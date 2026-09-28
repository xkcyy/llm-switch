package trae

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"llm-switch/internal/cdp"
	"llm-switch/internal/config"
)

// PlaceholderKey 是写入 Trae 的占位 Key：本机代理不校验，真实密钥留在 LLM Switch。
const PlaceholderKey = "llm-switch-local"

// addTimeout 是单个模型等待连通性测试的上限。
// Trae 的连通性测试会发一次真实请求，慢上游（实测 KmAiModelHub 走 responses 可达数分钟）需要放宽。
const addTimeout = 5 * time.Minute

// ErrBusy 表示已有同步任务在进行。
var ErrBusy = errors.New("已有同步任务正在进行")

// ItemResult 是单个模型的处理结果。
type ItemResult struct {
	Model  string `json:"model"`
	Result string `json:"result"` // present | added | saved_directly | exists | kept | deleted | failed
	Detail string `json:"detail,omitempty"`
}

// Job 是一次同步/断开任务的进度快照。
type Job struct {
	Kind       string       `json:"kind"`  // sync | disconnect
	State      string       `json:"state"` // running | done | failed
	Phase      string       `json:"phase"` // preparing | scanning | applying
	Message    string       `json:"message,omitempty"`
	Total      int          `json:"total"`
	Done       int          `json:"done"`
	Current    string       `json:"current,omitempty"`
	Items      []ItemResult `json:"items"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
}

// ModelInfo 是页面上展示用的模型摘要（来自 LLM Switch 的模型元数据）。
type ModelInfo struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"context_window"`
	Thinking      bool   `json:"thinking"`
}

// Status 是页面需要的 Trae 视图。
type Status struct {
	Installed       bool        `json:"installed"`
	Exe             string      `json:"exe"`
	UserDataDir     string      `json:"user_data_dir"`
	ArgvJSON        string      `json:"argv_json"`
	DebugPort       int         `json:"debug_port"`
	DebugConfigured bool        `json:"debug_configured"`
	Running         bool        `json:"running"`
	Ready           bool        `json:"ready"`        // 调试端口可用，可以直接同步
	NeedRestart     bool        `json:"need_restart"` // 正在运行但没有调试端口
	BaseURL         string      `json:"base_url"`
	Models          []string    `json:"models"`
	ModelDetails    []ModelInfo `json:"model_details"`
	Job             *Job        `json:"job,omitempty"`
}

// Service 是 Trae 接入服务。
type Service struct {
	store *config.Store
	paths Paths

	mu  sync.Mutex
	job *Job
}

// driverAPI 是接入编排对界面驱动的依赖（seam）。
// 生产实现是 CDP 驱动 *Driver；测试用内存实现，让编排逻辑
// （先删后加、逆序补齐、复核结论）不必连真机就能验证。
type driverAPI interface {
	Close()
	EnsureModelsPage(ctx context.Context) error
	ReadCustomModels(ctx context.Context) ([]string, error)
	AddModel(ctx context.Context, spec ModelSpec, baseURL, apiKey string, timeout time.Duration) (AddResult, error)
	DeleteModel(ctx context.Context, display string) error
}

func NewService(store *config.Store) *Service {
	return &Service{store: store, paths: DefaultPaths()}
}

func (s *Service) Paths() Paths { return s.paths }

// BaseURL 返回写进 Trae 的「自定义请求地址」。
// 不带 /chat/completions：「完整 URL」开关默认关闭，Trae 会自动补路径。
func (s *Service) BaseURL() string {
	cfg := s.store.Snapshot()
	return fmt.Sprintf("http://%s:%d/v1", cfg.Settings.Proxy.Host, cfg.Settings.Proxy.Port)
}

// Models 返回要同步到 Trae 的模型键（供应商ID/模型ID），与代理的 /v1/models 一致。
func (s *Service) Models() []string {
	cfg := s.store.Snapshot()
	out := []string{}
	for _, a := range cfg.AvailableModels() {
		out = append(out, a.Slug)
	}
	return out
}

// Status 汇总当前状态（不触发任何界面操作）。
func (s *Service) Status(ctx context.Context) Status {
	st := Status{
		Installed:       s.paths.Installed(),
		Exe:             s.paths.Exe,
		UserDataDir:     s.paths.UserDataDir,
		ArgvJSON:        s.paths.ArgvJSON,
		DebugPort:       s.paths.DebugPort(),
		DebugConfigured: s.paths.DebugPortConfigured(),
		BaseURL:         s.BaseURL(),
		Models:          s.Models(),
	}
	if !st.Installed {
		return st
	}
	for _, id := range st.Models {
		spec := s.SpecFor(id)
		st.ModelDetails = append(st.ModelDetails, ModelInfo{
			ID:            id,
			ContextWindow: spec.Context,
			Thinking:      spec.Thinking != nil && *spec.Thinking,
		})
	}
	st.Ready = cdp.Alive(ctx, st.DebugPort)
	st.Running = st.Ready || s.paths.Running(ctx)
	st.NeedRestart = st.Running && !st.Ready
	s.mu.Lock()
	if s.job != nil {
		job := *s.job
		job.Items = append([]ItemResult(nil), s.job.Items...)
		st.Job = &job
	}
	s.mu.Unlock()
	return st
}

// Progress 返回当前任务快照（没有任务时为 nil）。
func (s *Service) Progress() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return nil
	}
	job := *s.job
	job.Items = append([]ItemResult(nil), s.job.Items...)
	return &job
}

func (s *Service) setJob(mut func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil {
		mut(s.job)
	}
}

// StartSync 让 Trae 的模型列表与 LLM Switch 对齐（补齐缺失 + 删除多余）。
func (s *Service) StartSync() error {
	return s.start("sync")
}

// StartReorder 重建 Trae 列表顺序（按代理配置顺序，同供应商相邻）。较慢。
func (s *Service) StartReorder() error {
	return s.start("reorder")
}

func (s *Service) start(kind string) error {
	s.mu.Lock()
	if s.job != nil && s.job.State == "running" {
		s.mu.Unlock()
		return ErrBusy
	}
	job := &Job{
		Kind:      kind,
		State:     "running",
		Phase:     "preparing",
		Items:     []ItemResult{},
		StartedAt: time.Now(),
	}
	s.job = job
	s.mu.Unlock()

	go s.run(job.Kind)
	return nil
}

func (s *Service) run(kind string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	fail := func(msg string) {
		s.setJob(func(j *Job) {
			j.State = "failed"
			j.Message = msg
			now := time.Now()
			j.FinishedAt = &now
		})
		slog.Warn("Trae 任务失败", "kind", kind, "message", msg)
	}
	done := func(msg string) {
		s.setJob(func(j *Job) {
			j.State = "done"
			j.Message = msg
			now := time.Now()
			j.FinishedAt = &now
		})
		slog.Info("Trae 任务完成", "kind", kind, "message", msg)
	}

	// 1) 准备调试端口
	s.setJob(func(j *Job) { j.Phase = "preparing" })
	if !cdp.Alive(ctx, s.paths.DebugPort()) {
		if _, err := s.paths.EnsureDebugPort(); err != nil {
			fail("写入 Trae 调试端口配置失败：" + err.Error())
			return
		}
		if s.paths.Running(ctx) {
			fail("Trae 正在运行但没有开启调试端口，需要重启 Trae 后再试")
			return
		}
		if err := s.paths.Launch(); err != nil {
			fail("启动 Trae 失败：" + err.Error())
			return
		}
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) && !cdp.Alive(ctx, s.paths.DebugPort()) {
			time.Sleep(1 * time.Second)
		}
		if !cdp.Alive(ctx, s.paths.DebugPort()) {
			fail("Trae 启动后调试端口仍未就绪")
			return
		}
	}

	// 2) 连上界面
	drv, err := attachDriver(ctx, s.paths.DebugPort())
	if err != nil {
		fail("连接 Trae 失败：" + err.Error())
		return
	}
	defer drv.Close()

	s.setJob(func(j *Job) { j.Phase = "scanning"; j.Message = "正在读取 Trae 的模型列表…" })
	if err := drv.EnsureModelsPage(ctx); err != nil {
		fail(err.Error())
		return
	}
	existing, err := drv.ReadCustomModels(ctx)
	if err != nil {
		fail("读取 Trae 模型列表失败：" + err.Error())
		return
	}

	targets := s.Models()
	if kind == "reorder" {
		s.reorderModels(ctx, drv, targets, existing, done, fail)
	} else {
		s.syncModels(ctx, drv, targets, existing, done, fail)
	}
}

// SplitDiff 计算差异：missing = 代理有而 Trae 没有的（按 targets 顺序）；
// extra = Trae 有而代理没有的（按 existing 顺序）。
func SplitDiff(targets, existing []string) (missing, extra []string) {
	have := map[string]bool{}
	for _, e := range existing {
		have[e] = true
	}
	known := map[string]bool{}
	for _, t := range targets {
		known[t] = true
		if !have[t] {
			missing = append(missing, t)
		}
	}
	for _, e := range existing {
		if !known[e] {
			extra = append(extra, e)
		}
	}
	return missing, extra
}

// orderMatches 判断「代理模型在 Trae 列表里的出现顺序」是否与配置顺序一致。
func orderMatches(targets, existing []string) bool {
	present := make([]string, 0, len(targets))
	known := map[string]bool{}
	for _, t := range targets {
		known[t] = true
	}
	for _, e := range existing {
		if known[e] {
			present = append(present, e)
		}
	}
	if len(present) != len(targets) {
		return false
	}
	for i := range targets {
		if present[i] != targets[i] {
			return false
		}
	}
	return true
}

// SpecFor 返回某个模型键（供应商ID/模型ID）对应的写入信息：
// 上下文窗口、思考模式来自 LLM Switch 的模型元数据；图片能力只有明确标注时才下发。
func (s *Service) SpecFor(id string) ModelSpec {
	spec := ModelSpec{ID: id}
	cfg := s.store.Snapshot()
	a, ok := cfg.LookupAvailable(id)
	if !ok {
		return spec
	}
	m := a.Model
	if m.ContextWindow != nil && *m.ContextWindow > 0 {
		spec.Context = *m.ContextWindow
	}
	if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
		spec.MaxOutput = *m.MaxOutputTokens
	}
	if m.Reasoning != nil && len(m.Reasoning.Levels) > 0 {
		yes := true
		spec.Thinking = &yes
	}
	if hasVisionCapability(m.Capabilities) {
		yes := true
		spec.Vision = &yes
	}
	return spec
}

// hasVisionCapability 判断能力列表里是否明确包含视觉能力。
// 注意：能力里只有 tools（或为空）时表示「未知」，此时不要动 Trae 的默认值。
func hasVisionCapability(caps []string) bool {
	for _, c := range caps {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "vision", "image", "multimodal", "image_input":
			return true
		}
	}
	return false
}

// applyAdds 逐个添加模型；返回 added/saved/failed 数量。
// 注意按传入顺序依次添加 —— Trae 的列表是「新加的排最前面」，
// 所以调用方会传入逆序，让最终展示顺序与配置顺序一致。
func (s *Service) applyAdds(ctx context.Context, drv driverAPI, models []string, base int) (added, saved, failed int) {
	for i, model := range models {
		s.setJob(func(j *Job) { j.Current = model; j.Done = base + i })
		res, err := drv.AddModel(ctx, s.SpecFor(model), s.BaseURL(), PlaceholderKey, addTimeout)
		item := ItemResult{Model: model, Result: res.Status, Detail: res.Detail}
		if err != nil {
			item.Result = "failed"
			item.Detail = err.Error()
		}
		switch item.Result {
		case "added":
			added++
		case "saved_directly":
			saved++
		case "exists":
			item.Result = "present" // 扫描之后被别处加上了
		default:
			failed++
		}
		s.setJob(func(j *Job) { j.Items = append(j.Items, item) })
		s.setJob(func(j *Job) { j.Done = base + i + 1 })
	}
	return added, saved, failed
}

// applyDeletes 逐个删除不想要的模型；返回 deleted/failed 数量。
func (s *Service) applyDeletes(ctx context.Context, drv driverAPI, models []string, base int) (deleted, failed int) {
	for i, model := range models {
		s.setJob(func(j *Job) { j.Current = model; j.Done = base + i })
		item := ItemResult{Model: model, Result: "deleted"}
		if err := drv.DeleteModel(ctx, model); err != nil {
			item.Result = "failed"
			item.Detail = err.Error()
			failed++
		} else {
			deleted++
		}
		s.setJob(func(j *Job) { j.Items = append(j.Items, item) })
		s.setJob(func(j *Job) { j.Done = base + i + 1 })
	}
	return deleted, failed
}

// syncModels 让 Trae 与代理对齐：补齐缺失、删除多余，并复核结果。
func (s *Service) syncModels(ctx context.Context, drv driverAPI, targets, existing []string, done, fail func(string)) {
	missing, extra := SplitDiff(targets, existing)
	inConfig := map[string]bool{}
	for _, t := range targets {
		inConfig[t] = true
	}

	s.setJob(func(j *Job) {
		j.Phase = "applying"
		j.Total = len(missing) + len(extra)
		j.Done = 0
		j.Items = nil
		for _, t := range targets {
			if inConfig[t] && contains(existing, t) {
				j.Items = append(j.Items, ItemResult{Model: t, Result: "present"})
			}
		}
		switch {
		case len(missing) == 0 && len(extra) == 0:
			j.Message = "Trae 上的模型已经与本地代理一致"
		case len(missing) > 0 && len(extra) > 0:
			j.Message = fmt.Sprintf("需要新增 %d 个、删除 %d 个", len(missing), len(extra))
		case len(missing) > 0:
			j.Message = fmt.Sprintf("需要新增 %d 个", len(missing))
		default:
			j.Message = fmt.Sprintf("需要删除 %d 个多余模型", len(extra))
		}
	})

	// 先删多余：避免它们与新增项交错，也保证顺序判断干净
	deleted, delFailed := s.applyDeletes(ctx, drv, extra, 0)
	// 再按逆序补齐：Trae 新加的排最前，逆序添加后最终顺序即配置顺序（同供应商相邻）
	reversed := make([]string, 0, len(missing))
	for i := len(missing) - 1; i >= 0; i-- {
		reversed = append(reversed, missing[i])
	}
	added, saved, addFailed := s.applyAdds(ctx, drv, reversed, len(extra))

	// 复核：以界面为准再读一次
	s.setJob(func(j *Job) { j.Phase = "verifying"; j.Current = "" })
	msg := s.reconcileMessage(ctx, drv, targets, deleted, delFailed, added+saved, addFailed)
	done(msg)
}

// reconcileMessage 复核界面状态并给出一句如实的结论。
func (s *Service) reconcileMessage(ctx context.Context, drv driverAPI, targets []string, deleted, delFailed, added, addFailed int) string {
	after, err := drv.ReadCustomModels(ctx)
	if err != nil {
		return fmt.Sprintf("已处理：新增 %d、删除 %d，但复核失败（%v）", added, deleted, err)
	}
	missing, extra := SplitDiff(targets, after)
	ok := len(missing) == 0 && len(extra) == 0
	ordered := orderMatches(targets, after)

	base := fmt.Sprintf("Trae 现有自定义模型 %d 个：本次新增 %d、删除 %d", len(after), added, deleted)
	if ok {
		if ordered {
			return base + "；已与本地代理一致（顺序按供应商排列）"
		}
		return base + "；已与本地代理一致（顺序未对齐，可点「重新排序」）"
	}
	detail := ""
	if len(missing) > 0 {
		detail += fmt.Sprintf("仍缺 %d 个（%s）", len(missing), strings.Join(missing, "、"))
	}
	if len(extra) > 0 {
		if detail != "" {
			detail += "；"
		}
		detail += fmt.Sprintf("仍多 %d 个（%s）", len(extra), strings.Join(extra, "、"))
	}
	return base + "；" + detail
}

// reorderModels 重建顺序：删掉所有代理模型后按逆序重新添加，
// 使 Trae 列表中的展示顺序与配置顺序（同供应商相邻）一致。
func (s *Service) reorderModels(ctx context.Context, drv driverAPI, targets, existing []string, done, fail func(string)) {
	if len(targets) == 0 {
		fail("本地代理没有可用模型，无法排序")
		return
	}
	known := map[string]bool{}
	for _, t := range targets {
		known[t] = true
	}
	present := make([]string, 0, len(existing))
	for _, e := range existing {
		if known[e] {
			present = append(present, e)
		}
	}
	_, extra := SplitDiff(targets, existing)

	s.setJob(func(j *Job) {
		j.Phase = "applying"
		j.Total = len(present) + len(extra) + len(targets)
		j.Done = 0
		j.Items = nil
		j.Message = fmt.Sprintf("将重建 %d 个模型的顺序（每个都会重跑一次连通性测试，可能需要几分钟）", len(targets))
	})

	// 1) 先清掉代理模型与多余项
	toDelete := append(append([]string{}, extra...), present...)
	deleted, delFailed := s.applyDeletes(ctx, drv, toDelete, 0)
	// 2) 逆序重建
	reversed := make([]string, 0, len(targets))
	for i := len(targets) - 1; i >= 0; i-- {
		reversed = append(reversed, targets[i])
	}
	added, saved, addFailed := s.applyAdds(ctx, drv, reversed, deleted)

	msg := s.reconcileMessage(ctx, drv, targets, deleted, delFailed, added+saved, addFailed)
	done(msg)
}

// contains 判断字符串切片里是否包含某值。
func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// Restart 重启 Trae 以开启调试端口（会中断正在运行的会话）。
func (s *Service) Restart(ctx context.Context) error {
	if _, err := s.paths.EnsureDebugPort(); err != nil {
		return err
	}
	if err := s.paths.Restart(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if cdp.Alive(ctx, s.paths.DebugPort()) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("Trae 重启后调试端口仍未就绪")
}

// EnsureDebugConfigured 写入调试端口配置（不重启），返回是否发生了改动。
func (s *Service) EnsureDebugConfigured() (bool, error) {
	return s.paths.EnsureDebugPort()
}

// RemoveDebugConfig 移除本插件写入的调试端口配置。
func (s *Service) RemoveDebugConfig() (bool, error) {
	return s.paths.RemoveDebugPort()
}

// Summarize 给出一条人类可读的一行摘要（用于页面提示）。
func (j *Job) Summarize() string {
	if j == nil {
		return ""
	}
	if j.Message != "" {
		return j.Message
	}
	return strings.TrimSpace(j.State)
}
