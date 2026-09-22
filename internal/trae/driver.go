package trae

import (
	"context"
	"fmt"
	"strings"
	"time"

	"llm-switch/internal/cdp"
)

// Driver 是 Trae 界面的自动化驱动（基于 CDP）。
//
// 所有步骤都与手工操作一一对应，选择器取自真机验证（非哈希类名 + 文案兜底）：
//
//	模型选择器 button.core-model-select-trigger（Radix，需真实鼠标事件）
//	弹层条目 .core-model-select-footer（文案「添加模型」）
//	设置页按钮 button.icd-btn（文案「添加模型」）
//	供应商列表条目（文案「自定义模型」）
//	表单 input[placeholder*=模型 ID] / input[placeholder*=api.openai.com/v1] / input[type=password]
//	提交 button.add-model-connect-button（测试中会 disabled）
//	测试失败的兜底 button（文案「直接保存」）
type Driver struct {
	cli  *cdp.Client
	port int
}

// attachDriver 连上 Trae 的页面目标。
func attachDriver(ctx context.Context, port int) (*Driver, error) {
	targets, err := cdp.ListTargets(ctx, port)
	if err != nil {
		return nil, fmt.Errorf("读取 Trae 调试目标失败：%w", err)
	}
	var picked *cdp.Target
	for i := range targets {
		t := targets[i]
		if t.Type != "page" || t.WebSocketDebuggerURL == "" {
			continue
		}
		if strings.Contains(t.Title, "TraeWork") || strings.Contains(t.URL, "solo-lite") {
			picked = &t
			break
		}
	}
	if picked == nil {
		for i := range targets {
			if targets[i].Type == "page" && targets[i].WebSocketDebuggerURL != "" {
				picked = &targets[i]
				break
			}
		}
	}
	if picked == nil {
		return nil, fmt.Errorf("没有找到 Trae 的页面（请确认 Trae 已打开）")
	}
	cli, err := cdp.Dial(ctx, picked.WebSocketDebuggerURL)
	if err != nil {
		return nil, err
	}
	return &Driver{cli: cli, port: port}, nil
}

// Close 关闭连接。
func (d *Driver) Close() { d.cli.Close() }

// ---- 通用 ----

func (d *Driver) waitBox(ctx context.Context, expr string, timeout time.Duration) ([]float64, error) {
	return d.waitBoxArgs(ctx, expr, nil, timeout)
}

// waitBoxArgs 与 waitBox 相同，但会把 args 传给页面脚本。
func (d *Driver) waitBoxArgs(ctx context.Context, expr string, args any, timeout time.Duration) ([]float64, error) {
	deadline := time.Now().Add(timeout)
	for {
		var box []float64
		if err := d.cli.Eval(ctx, expr, args, &box); err != nil {
			return nil, err
		}
		if len(box) == 2 && box[0] > 0 && box[1] > 0 {
			return box, nil
		}
		if time.Now().After(deadline) {
			return nil, errTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

var errTimeout = fmt.Errorf("等待界面元素超时")

func (d *Driver) sleep(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// ---- 页面探测 ----

const jsHasForm = `(() => !!document.querySelector('input[placeholder*="模型 ID"], input[placeholder*="Model ID"]'))()`

const jsSettingsAddButton = `(() => {
  for (const el of document.querySelectorAll('button')) {
    const t = (el.textContent || '').trim();
    if (t !== '添加模型' && t !== '+ 添加模型') continue;
    if (el.classList.contains('add-model-connect-button')) continue;
    const r = el.getBoundingClientRect();
    if (r.width > 20 && r.height > 10) return [r.x + r.width / 2, r.y + r.height / 2];
  }
  return null;
})()`

const jsProviderCustomBox = `(() => {
  for (const el of document.querySelectorAll('button,[role=button],div,span')) {
    const t = (el.textContent || '').trim();
    if (t !== '自定义模型' && t !== 'Custom Model') continue;
    if (el.children.length > 3) continue;
    const r = el.getBoundingClientRect();
    if (r.width > 40 && r.height > 20) return [r.x + r.width / 2, r.y + r.height / 2];
  }
  return null;
})()`

const jsModelTriggerBox = `(() => {
  const el = document.querySelector('button.core-model-select-trigger');
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return [r.x + r.width / 2, r.y + r.height / 2];
})()`

const jsPopupAddBox = `(() => {
  const pop = document.querySelector('[data-radix-popper-content-wrapper]');
  if (!pop) return null;
  for (const el of pop.querySelectorAll('*')) {
    const t = (el.textContent || '').trim();
    if (t !== '添加模型' && t !== 'Add Model') continue;
    if (el.children.length > 2) continue;
    const r = el.getBoundingClientRect();
    if (r.width > 20) return [r.x + r.width / 2, r.y + r.height / 2];
  }
  return null;
})()`

const jsSubmitState = `(() => {
  const b = document.querySelector('button.add-model-connect-button');
  if (!b) return null;
  return { disabled: !!b.disabled, text: (b.innerText || '').trim() };
})()`

const jsSubmitBox = `(() => {
  const b = document.querySelector('button.add-model-connect-button');
  if (!b) return null;
  const r = b.getBoundingClientRect();
  return [r.x + r.width / 2, r.y + r.height / 2];
})()`

const jsSaveDirectlyBox = `(() => {
  for (const el of document.querySelectorAll('button,[role=button]')) {
    const t = (el.textContent || '').trim();
    if (t !== '直接保存' && t !== 'Save Directly') continue;
    const r = el.getBoundingClientRect();
    if (r.width > 10) return [r.x + r.width / 2, r.y + r.height / 2];
  }
  return null;
})()`

const jsDirty = `(() => {
  for (const el of document.querySelectorAll('input,textarea')) { if (el.value) return true; }
  return false;
})()`

const jsReset = `(() => {
  const b = document.querySelector('button.add-model-reset-button');
  if (b) { b.click(); return true; }
  return false;
})()`

const jsReadRows = `(() => {
  const rows = [];
  for (const tr of document.querySelectorAll('tr')) {
    const tds = tr.querySelectorAll('td');
    if (tds.length < 3) continue;
    const name = ((tds[0].innerText || tds[0].textContent || '') + '').trim();
    const vendor = ((tds[1].innerText || tds[1].textContent || '') + '').trim();
    if (!name) continue;
    let del = null;
    const action = tds[tds.length - 1];
    for (const el of action.querySelectorAll('span')) {
      const cls = (el.className || '').toString();
      if (cls.indexOf('Delete2') >= 0) {
        const r = el.getBoundingClientRect();
        if (r.width > 0) { del = [r.x + r.width / 2, r.y + r.height / 2]; break; }
      }
    }
    rows.push({ name: name, vendor: vendor, del: del });
  }
  return rows;
})()`

const jsConfirmDeleteBox = `(() => {
  for (const el of document.querySelectorAll('button,[role=button]')) {
    const t = (el.textContent || '').trim();
    if (t !== '删除' && t !== 'Delete') continue;
    const r = el.getBoundingClientRect();
    if (r.width > 20) return [r.x + r.width / 2, r.y + r.height / 2];
  }
  return null;
})()`

const jsFill = `((args) => {
  const sel = args[0], val = args[1];
  const el = document.querySelector(sel);
  if (!el) return false;
  const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
  setter.call(el, val);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
})`

// ---- 高层步骤 ----

type modelRow struct {
	Name   string    `json:"name"`
	Vendor string    `json:"vendor"`
	Del    []float64 `json:"del"`
}

func (d *Driver) hasForm(ctx context.Context) bool {
	var ok bool
	_ = d.cli.Eval(ctx, jsHasForm, nil, &ok)
	return ok
}

// CloseDialogs 尝试关掉可能开着的弹窗（Esc 两次），失败忽略。
func (d *Driver) CloseDialogs(ctx context.Context) {
	for i := 0; i < 2; i++ {
		if err := d.cli.Key(ctx, "Escape"); err != nil {
			return
		}
		d.sleep(500)
	}
}

// EnsureModelsPage 确保停在「设置 → 模型」页面（表单已打开也算达标）。
func (d *Driver) EnsureModelsPage(ctx context.Context) error {
	if d.hasForm(ctx) {
		return nil
	}
	if box, _ := d.waitBox(ctx, jsSettingsAddButton, 800*time.Millisecond); box != nil {
		return nil // 已在模型设置页
	}
	// 可能停在设置里的其它子页（账号、MCP…），先关掉弹窗回到聊天页再走标准入口
	var dialogOpen bool
	_ = d.cli.Eval(ctx, `(() => !!document.querySelector('[role="dialog"]'))()`, nil, &dialogOpen)
	if dialogOpen {
		d.CloseDialogs(ctx)
		d.sleep(700)
	}
	if d.hasForm(ctx) {
		return nil
	}
	if box, _ := d.waitBox(ctx, jsSettingsAddButton, 800*time.Millisecond); box != nil {
		return nil
	}
	// 从聊天页进入：模型选择器 → 弹层「添加模型」
	trigger, err := d.waitBox(ctx, jsModelTriggerBox, 15*time.Second)
	if err != nil {
		return fmt.Errorf("找不到模型选择器（Trae 界面可能已变化）：%w", err)
	}
	if err := d.cli.Click(ctx, trigger[0], trigger[1]); err != nil {
		return err
	}
	d.sleep(900)
	item, err := d.waitBox(ctx, jsPopupAddBox, 10*time.Second)
	if err != nil {
		return fmt.Errorf("模型选择器弹层里找不到「添加模型」：%w", err)
	}
	if err := d.cli.Click(ctx, item[0], item[1]); err != nil {
		return err
	}
	if _, err := d.waitBox(ctx, jsSettingsAddButton, 20*time.Second); err != nil {
		return fmt.Errorf("未能打开 Trae 的模型设置页：%w", err)
	}
	return nil
}

// OpenCustomForm 确保「自定义模型」表单处于打开状态。
func (d *Driver) OpenCustomForm(ctx context.Context) error {
	if d.hasForm(ctx) {
		return nil
	}
	if box, _ := d.waitBox(ctx, jsProviderCustomBox, 1500*time.Millisecond); box != nil {
		if err := d.cli.Click(ctx, box[0], box[1]); err != nil {
			return err
		}
		d.sleep(1200)
		if d.hasForm(ctx) {
			return nil
		}
	}
	addBox, err := d.waitBox(ctx, jsSettingsAddButton, 5*time.Second)
	if err != nil {
		return fmt.Errorf("模型设置页找不到「添加模型」按钮：%w", err)
	}
	if err := d.cli.Click(ctx, addBox[0], addBox[1]); err != nil {
		return err
	}
	d.sleep(1500)
	box, err := d.waitBox(ctx, jsProviderCustomBox, 10*time.Second)
	if err != nil {
		return fmt.Errorf("供应商列表里找不到「自定义模型」：%w", err)
	}
	if err := d.cli.Click(ctx, box[0], box[1]); err != nil {
		return err
	}
	d.sleep(1200)
	if !d.hasForm(ctx) {
		return fmt.Errorf("自定义模型表单没有出现（Trae 界面可能已变化）")
	}
	return nil
}

// ReadCustomModels 读出设置页里的自定义模型（显示名，按界面顺序）。显示名即模型 ID。
//
// 只认「设置 → 模型」表格里的行：这类行带删除图标，聊天区里的 Markdown 表格会被排除。
func (d *Driver) ReadCustomModels(ctx context.Context) ([]string, error) {
	rows, err := d.waitTableRows(ctx, 20*time.Second)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r.Del) != 2 || !isCustomVendor(r.Vendor) {
			continue
		}
		out = append(out, r.Name)
	}
	return out, nil
}

// waitTableRows 等模型表格渲染完成后再读。
//
// Trae 的设置页先渲染壳、表格数据后到；如果读得太早会得到 0 行，
// 导致「明明有模型却判定为没有」，同步会重复添加、清理会漏删。
// 这里要求「连续两次读到的行数一致」才认为稳定。
func (d *Driver) waitTableRows(ctx context.Context, timeout time.Duration) ([]modelRow, error) {
	deadline := time.Now().Add(timeout)
	var last []modelRow
	stable := 0
	for {
		rows, err := d.readRows(ctx)
		if err != nil {
			return nil, err
		}
		table := make([]modelRow, 0, len(rows))
		for _, r := range rows {
			if len(r.Del) == 2 {
				table = append(table, r)
			}
		}
		if len(table) > 0 && len(table) == len(last) {
			stable++
			if stable >= 1 {
				return table, nil
			}
		} else {
			stable = 0
		}
		last = table
		if time.Now().After(deadline) {
			if len(table) == 0 {
				return nil, fmt.Errorf("Trae 的模型列表没有加载出来（请确认已登录，且能正常打开「设置 → 模型」）")
			}
			return table, nil
		}
		d.sleep(400)
	}
}

// verifyPresent 等列表里出现某个模型（界面刷新有延迟）。
func (d *Driver) verifyPresent(ctx context.Context, modelID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		models, err := d.ReadCustomModels(ctx)
		if err == nil {
			for _, m := range models {
				if m == modelID {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		d.sleep(700)
	}
}

func isCustomVendor(v string) bool {
	return strings.Contains(v, "自定义") || strings.Contains(strings.ToLower(v), "custom")
}

func (d *Driver) readRows(ctx context.Context) ([]modelRow, error) {
	var rows []modelRow
	if err := d.cli.Eval(ctx, jsReadRows, nil, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ModelSpec 是写入 Trae 时需要带上的模型信息（拿不到的字段留零值/ nil，表示不设置）。
type ModelSpec struct {
	ID        string
	Context   int   // 上下文窗口（输入）
	MaxOutput int   // 上下文窗口（输出）
	Vision    *bool // 是否支持图片输入；nil = 不设置
	Thinking  *bool // 是否开启思考模式；nil = 跟随模型默认配置
}

const jsAdvancedHeaderBox = `(() => {
  const el = document.querySelector('.add-model-advanced-header');
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return [r.x + r.width / 2, r.y + r.height / 2];
})()`

// 高级配置是否已展开：展开后 DOM 里才有「上下文窗口」表单块。
const jsAdvancedOpen = `(() => {
  const dlg = document.querySelector('.add-model-dialog');
  if (!dlg) return false;
  for (const it of dlg.querySelectorAll('[class*=form-item]')) {
    const t = it.textContent || '';
    if (t.indexOf('上下文窗口') >= 0 || t.indexOf('Context Window') >= 0) return true;
  }
  return false;
})()`

const jsSetContextWindow = `((args) => {
  const ctxVal = args[0], outVal = args[1];
  const dlg = document.querySelector('.add-model-dialog');
  if (!dlg) return 'no-dialog';
  let item = null;
  for (const it of dlg.querySelectorAll('[class*=form-item]')) {
    const t = it.textContent || '';
    if (t.indexOf('上下文窗口') >= 0 || t.indexOf('Context Window') >= 0) { item = it; break; }
  }
  if (!item) return 'no-item';
  const inputs = [...item.querySelectorAll('input')];
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
  const set = (el, v) => {
    if (!el || !v || v <= 0) return;
    setter.call(el, String(v));
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
  };
  set(inputs[0], ctxVal);
  set(inputs[1], outVal);
  return 'ok:' + inputs.length;
})`

// 返回单选项的点击坐标：先按文案匹配，匹配不到时按序号兜底。
const jsRadioBox = `((args) => {
  const groupLabel = args[0], optionText = args[1], optionIndex = args[2];
  const dlg = document.querySelector('.add-model-dialog');
  if (!dlg) return null;
  let item = null;
  for (const it of dlg.querySelectorAll('[class*=form-item]')) {
    const t = it.textContent || '';
    if (t.indexOf(groupLabel) >= 0) { item = it; break; }
  }
  if (!item) return null;
  const radios = [...item.querySelectorAll('input[type=radio]')];
  const pick = (r) => {
    const b = r.getBoundingClientRect();
    if (b.width > 0 && b.height > 0) return [b.x + b.width / 2, b.y + b.height / 2];
    const wrap = r.closest('label') || r.parentElement;
    if (wrap) {
      const w = wrap.getBoundingClientRect();
      if (w.width > 0) return [w.x + w.height / 2, w.y + w.height / 2];
    }
    return null;
  };
  for (const r of radios) {
    const wrap = r.closest('label') || r.parentElement;
    const t = wrap ? (wrap.textContent || '').trim() : '';
    if (optionText && t.indexOf(optionText) >= 0) return pick(r);
  }
  if (optionIndex >= 0 && optionIndex < radios.length) return pick(radios[optionIndex]);
  return null;
})`

const jsDialogText = `(() => {
  const d = document.querySelector('.add-model-dialog');
  return d ? (d.textContent || '') : '';
})()`

// ensureAdvancedOpen 展开「高级配置」。
func (d *Driver) ensureAdvancedOpen(ctx context.Context) error {
	var open bool
	if err := d.cli.Eval(ctx, jsAdvancedOpen, nil, &open); err != nil {
		return err
	}
	if open {
		return nil
	}
	box, err := d.waitBox(ctx, jsAdvancedHeaderBox, 5*time.Second)
	if err != nil {
		return fmt.Errorf("找不到「高级配置」入口：%w", err)
	}
	if err := d.cli.Click(ctx, box[0], box[1]); err != nil {
		return err
	}
	d.sleep(900)
	if err := d.cli.Eval(ctx, jsAdvancedOpen, nil, &open); err != nil {
		return err
	}
	if !open {
		return fmt.Errorf("「高级配置」没有展开")
	}
	return nil
}

// applyAdvanced 按模型信息填写高级配置（拿不到的信息保持 Trae 默认值）。
func (d *Driver) applyAdvanced(ctx context.Context, spec ModelSpec) error {
	if spec.Context <= 0 && spec.MaxOutput <= 0 && spec.Vision == nil && spec.Thinking == nil {
		return nil
	}
	if err := d.ensureAdvancedOpen(ctx); err != nil {
		return err
	}
	if spec.Context > 0 || spec.MaxOutput > 0 {
		var res string
		if err := d.cli.Eval(ctx, jsSetContextWindow, []any{spec.Context, spec.MaxOutput}, &res); err != nil {
			return err
		}
		if !strings.HasPrefix(res, "ok") {
			return fmt.Errorf("填写上下文窗口失败（%s）", res)
		}
	}
	if spec.Vision != nil {
		text, idx := "支持", 0
		if !*spec.Vision {
			text, idx = "不支持", 1
		}
		if err := d.clickRadio(ctx, "支持图片输入", text, idx); err != nil {
			return err
		}
	}
	if spec.Thinking != nil {
		text, idx := "开启", 1
		if !*spec.Thinking {
			text, idx = "关闭", 2
		}
		if err := d.clickRadio(ctx, "思考模式", text, idx); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) clickRadio(ctx context.Context, group, option string, index int) error {
	box, err := d.waitBoxArgs(ctx, jsRadioBox, []any{group, option, index}, 3*time.Second)
	if err != nil {
		return fmt.Errorf("找不到「%s → %s」选项", group, option)
	}
	if err := d.cli.Click(ctx, box[0], box[1]); err != nil {
		return err
	}
	d.sleep(200)
	return nil
}

// imageUnsupportedHint 判断弹窗里是否提示「模型不支持图片输入」。
func (d *Driver) imageUnsupportedHint(ctx context.Context) bool {
	var txt string
	if err := d.cli.Eval(ctx, jsDialogText, nil, &txt); err != nil {
		return false
	}
	low := strings.ToLower(txt)
	return strings.Contains(low, "does not support image") ||
		strings.Contains(low, "not support image inputs") ||
		strings.Contains(txt, "不支持图片输入")
}

// AddResult 是一次添加的结果。
type AddResult struct {
	Status string // added | saved_directly | exists | failed
	Detail string
}

// waitIdle 若上一次的连通性测试还在跑（按钮显示「测试中」且不可用），等它结束。
func (d *Driver) waitIdle(ctx context.Context, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var st *struct {
			Disabled bool   `json:"disabled"`
			Text     string `json:"text"`
		}
		if err := d.cli.Eval(ctx, jsSubmitState, nil, &st); err != nil {
			return
		}
		if st == nil {
			return // 没有表单（上一次已成功关闭）
		}
		busy := st.Disabled && (strings.Contains(st.Text, "测试") || strings.Contains(strings.ToLower(st.Text), "test"))
		if !busy {
			return
		}
		d.sleep(700)
	}
}

// AddModel 走官方流程添加一个自定义模型。
//
//	baseURL  不带 /chat/completions；「完整 URL」开关默认关闭，Trae 会补 /chat/completions
//	spec     模型信息：上下文窗口 / 思考模式 / 图片输入会写进「高级配置」
func (d *Driver) AddModel(ctx context.Context, spec ModelSpec, baseURL, apiKey string, timeout time.Duration) (AddResult, error) {
	modelID := spec.ID
	if err := d.EnsureModelsPage(ctx); err != nil {
		return AddResult{Status: "failed"}, err
	}
	d.waitIdle(ctx, 2*time.Minute)
	if !d.hasForm(ctx) {
		if err := d.OpenCustomForm(ctx); err != nil {
			return AddResult{Status: "failed"}, err
		}
	}
	// 清理可能残留的上一次内容
	var dirty bool
	_ = d.cli.Eval(ctx, jsDirty, nil, &dirty)
	if dirty {
		_ = d.cli.Eval(ctx, jsReset, nil, nil)
		d.sleep(700)
	}
	// 填充（JS 原生 setter + input 事件，已实测能更新 React 状态）
	fields := [][2]string{
		{`input[placeholder*="api.openai.com/v1"], input[placeholder*="https://"]`, baseURL},
		{`input[placeholder*="模型 ID"], input[placeholder*="Model ID"]`, modelID},
		{`input[placeholder*="展示名称"], input[placeholder*="isplay name"]`, modelID},
		{`input[type="password"]`, apiKey},
	}
	for _, f := range fields {
		var ok bool
		if err := d.cli.Eval(ctx, jsFill, []string{f[0], f[1]}, &ok); err != nil {
			return AddResult{Status: "failed"}, err
		}
		if !ok {
			return AddResult{Status: "failed"}, fmt.Errorf("找不到表单字段：%s", f[0])
		}
	}
	d.sleep(500)

	// 高级配置：上下文窗口 / 思考模式 / 图片输入
	if err := d.applyAdvanced(ctx, spec); err != nil {
		return AddResult{Status: "failed"}, err
	}
	d.sleep(300)

	// 等提交按钮可用
	deadline := time.Now().Add(20 * time.Second)
	for {
		var st *struct {
			Disabled bool   `json:"disabled"`
			Text     string `json:"text"`
		}
		if err := d.cli.Eval(ctx, jsSubmitState, nil, &st); err != nil {
			return AddResult{Status: "failed"}, err
		}
		if st == nil {
			return AddResult{Status: "failed"}, fmt.Errorf("找不到「添加模型」提交按钮")
		}
		if !st.Disabled {
			break
		}
		if time.Now().After(deadline) {
			return AddResult{Status: "failed", Detail: "提交按钮不可用（表单校验未通过：模型 ID 重复或字段不合法）"}, nil
		}
		d.sleep(400)
	}

	submit, err := d.waitBox(ctx, jsSubmitBox, 5*time.Second)
	if err != nil {
		return AddResult{Status: "failed"}, fmt.Errorf("找不到「添加模型」提交按钮")
	}
	if err := d.cli.Click(ctx, submit[0], submit[1]); err != nil {
		return AddResult{Status: "failed"}, err
	}

	// 等结果：弹窗关闭 = 成功；出现「直接保存」= 连通性测试失败
	imageRetried := spec.Vision != nil // 已经明确设过图片能力就不再重试
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		d.sleep(700)
		if !d.hasForm(ctx) {
			if d.verifyPresent(ctx, modelID, 10*time.Second) {
				return AddResult{Status: "added"}, nil
			}
			return AddResult{Status: "failed", Detail: "弹窗已关闭但列表里没有出现该模型，请重试"}, nil
		}
		if box, _ := d.waitBox(ctx, jsSaveDirectlyBox, 200*time.Millisecond); box != nil {
			// 测试失败但原因是「模型不支持图片输入」：把图片输入改成「不支持」再试一次，
			// 这样既能让测试通过，也能把正确的能力写进 Trae。
			if !imageRetried && d.imageUnsupportedHint(ctx) {
				imageRetried = true
				no := false
				spec.Vision = &no
				if err := d.applyAdvanced(ctx, spec); err != nil {
					// 改不了就按原来的兜底走（点击「直接保存」）
				} else {
					d.sleep(400)
					if sb, err := d.waitBox(ctx, jsSubmitBox, 5*time.Second); err == nil {
						_ = d.cli.Click(ctx, sb[0], sb[1])
						continue
					}
				}
			}
			if err := d.cli.Click(ctx, box[0], box[1]); err != nil {
				return AddResult{Status: "failed"}, err
			}
			for i := 0; i < 30; i++ {
				d.sleep(400)
				if !d.hasForm(ctx) {
					if d.verifyPresent(ctx, modelID, 10*time.Second) {
						return AddResult{Status: "saved_directly"}, nil
					}
					return AddResult{Status: "failed", Detail: "点击「直接保存」后列表里没有出现该模型"}, nil
				}
			}
			return AddResult{Status: "failed", Detail: "点击「直接保存」后弹窗未关闭"}, nil
		}
		if exists := d.existsHint(ctx); exists {
			return AddResult{Status: "exists"}, nil
		}
	}
	return AddResult{Status: "failed", Detail: "等待连通性测试超时（可稍后重试，或检查该模型在上游是否可用）"}, nil
}

// existsHint 判断「添加模型」弹窗里是否提示「模型已存在」。
// 注意只看弹窗内部：聊天区历史消息里也可能出现「已存在」字样，扫全页会误判。
func (d *Driver) existsHint(ctx context.Context) bool {
	var txt string
	_ = d.cli.Eval(ctx, `(() => {
      const dlg = document.querySelector('.add-model-dialog');
      return dlg ? (dlg.textContent || '') : '';
    })()`, nil, &txt)
	return strings.Contains(txt, "已存在") || strings.Contains(strings.ToLower(txt), "already exists")
}

// CloseOverlays 关掉可能盖在表格上的弹窗（例如停在「供应商选择」或表单）。
func (d *Driver) CloseOverlays(ctx context.Context) {
	for i := 0; i < 3; i++ {
		var open bool
		if err := d.cli.Eval(ctx, `(() => !!document.querySelector('.icd-modal-overlay, [role="dialog"]'))()`, nil, &open); err != nil {
			return
		}
		if !open {
			return
		}
		if err := d.cli.Key(ctx, "Escape"); err != nil {
			return
		}
		d.sleep(600)
	}
}

// DeleteModel 删除一个自定义模型（按显示名定位那一行）。
func (d *Driver) DeleteModel(ctx context.Context, display string) error {
	// 弹窗会盖住表格，导致点不到行内的删除图标：先关干净再定位
	d.CloseOverlays(ctx)
	if err := d.EnsureModelsPage(ctx); err != nil {
		return err
	}
	rows, err := d.waitTableRows(ctx, 20*time.Second)
	if err != nil {
		return err
	}
	var target *modelRow
	for i := range rows {
		if rows[i].Name == display && isCustomVendor(rows[i].Vendor) && len(rows[i].Del) == 2 {
			target = &rows[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("在 Trae 模型列表里找不到「%s」", display)
	}
	if err := d.cli.Click(ctx, target.Del[0], target.Del[1]); err != nil {
		return err
	}
	d.sleep(900)
	confirm, err := d.waitBox(ctx, jsConfirmDeleteBox, 8*time.Second)
	if err != nil {
		return fmt.Errorf("删除确认框没有出现")
	}
	if err := d.cli.Click(ctx, confirm[0], confirm[1]); err != nil {
		return err
	}
	d.sleep(1200)
	return nil
}
