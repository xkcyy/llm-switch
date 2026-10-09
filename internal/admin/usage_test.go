package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/dsh"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/trae"
	"llm-switch/internal/usage"
	"llm-switch/internal/workbuddy"
)

func newUsageServer(t *testing.T) (*http.ServeMux, *usage.Store) {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("WORKBUDDY_CONFIG_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := &proxy.Holder{}
	index.Store(proxy.BuildIndex(&config.Config{}))
	px := proxy.NewServer(store, index)
	svc := New(store, provider.NewService(store, nil), px,
		codex.NewService(store), opencode.NewService(store), trae.NewService(store), dsh.NewService(store),
		workbuddy.NewService(store),
		fstest.MapFS{}, t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.registerAPI(mux)
	t.Cleanup(func() { _ = px.Usage().Close() })
	return mux, px.Usage()
}

type report struct {
	Granularity string              `json:"granularity"`
	Group       string              `json:"group"`
	Totals      usage.Totals        `json:"totals"`
	Series      []usage.SeriesPoint `json:"series"`
	Groups      []usage.GroupRow    `json:"groups"`
}

func getReport(t *testing.T, mux *http.ServeMux, query string) report {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/report?"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询 %s 返回 %d：%s", query, rec.Code, rec.Body.String())
	}
	var out report
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func periodsOf(series []usage.SeriesPoint) map[string]bool {
	periods := map[string]bool{}
	for _, p := range series {
		periods[p.Period] = true
	}
	return periods
}

func TestUsageReportGroupsByModel(t *testing.T) {
	mux, st := newUsageServer(t)
	now := time.Now()
	st.Add(usage.Entry{Time: now, ProviderID: "km", ProviderName: "KmAiModelHub", ModelID: "glm-5.3", ModelName: "GLM-5.3",
		Caller: "Codex", OK: true, HasUsage: true, Input: 100, Output: 20, Total: 120})
	st.Add(usage.Entry{Time: now, ProviderID: "ocg", ProviderName: "OpenCode Go", ModelID: "glm-5.3", ModelName: "GLM-5.3",
		Caller: "OpenCode", OK: false})

	out := getReport(t, mux, "range=30d&group=model")
	if out.Granularity != usage.GranDay || len(periodsOf(out.Series)) != 30 {
		t.Fatalf("近 30 天应按天 30 个点：granularity=%s periods=%d", out.Granularity, len(periodsOf(out.Series)))
	}
	if len(out.Groups) != 2 {
		t.Fatalf("不同供应商下的同名模型应分两行，实际 %d", len(out.Groups))
	}
	if len(out.Series) != 60 {
		t.Fatalf("序列应为 分组×时段 = 60 点，实际 %d", len(out.Series))
	}
	if out.Totals.Requests != 2 || out.Totals.Err != 1 || out.Totals.Total != 120 {
		t.Fatalf("合计错误：%+v", out.Totals)
	}
	top := out.Groups[0]
	if top.ModelName != "GLM-5.3" || top.ProviderName != "KmAiModelHub" || top.Total != 120 {
		t.Fatalf("首行错误：%+v", top)
	}
}

func TestUsageReportGroupAll(t *testing.T) {
	mux, st := newUsageServer(t)
	now := time.Now()
	st.Add(usage.Entry{Time: now, ProviderID: "km", ModelID: "a", OK: true, HasUsage: true, Total: 10})
	st.Add(usage.Entry{Time: now, ProviderID: "ocg", ModelID: "b", OK: true, HasUsage: true, Total: 7})

	out := getReport(t, mux, "range=7d&group=all")
	if out.Group != usage.GroupAll || len(out.Groups) != 1 {
		t.Fatalf("汇总应只有一行，实际 group=%s groups=%d", out.Group, len(out.Groups))
	}
	if len(out.Series) != 7 {
		t.Fatalf("汇总应只有一条 7 点序列，实际 %d", len(out.Series))
	}
	if out.Groups[0].Total != 17 || out.Totals.Total != 17 {
		t.Fatalf("汇总合计错误：%+v", out.Groups[0])
	}
}

func TestUsageReportRangesAndGranularity(t *testing.T) {
	mux, st := newUsageServer(t)
	now := time.Now()
	st.Add(usage.Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Total: 5})

	today := getReport(t, mux, "range=today&group=model")
	if today.Granularity != usage.GranHour || len(today.Series) != 24 {
		t.Fatalf("当天应按小时 24 点：granularity=%s points=%d", today.Granularity, len(today.Series))
	}

	week := getReport(t, mux, "range=7d&group=model")
	if len(periodsOf(week.Series)) != 7 {
		t.Fatalf("近 7 天应有 7 个点，实际 %d", len(periodsOf(week.Series)))
	}

	month := getReport(t, mux, "range=month&group=model")
	if month.Granularity != usage.GranDay || len(periodsOf(month.Series)) != now.Day() {
		t.Fatalf("本月应按天到当天：granularity=%s periods=%d（今天 %d 号）",
			month.Granularity, len(periodsOf(month.Series)), now.Day())
	}

	narrow := getReport(t, mux, "range=custom&group=model&from="+
		now.AddDate(0, 0, -9).Format("2006-01-02")+"&to="+now.AddDate(0, 0, -7).Format("2006-01-02"))
	if narrow.Granularity != usage.GranDay || len(periodsOf(narrow.Series)) != 3 {
		t.Fatalf("窄跨度应按日 3 点：granularity=%s periods=%d", narrow.Granularity, len(periodsOf(narrow.Series)))
	}

	wide := getReport(t, mux, "range=custom&group=model&from="+
		now.AddDate(0, 0, -200).Format("2006-01-02")+"&to="+now.Format("2006-01-02"))
	if wide.Granularity != usage.GranMonth {
		t.Fatalf("跨度超一个月应转月粒度：%s", wide.Granularity)
	}
}

func TestUsageSummaryEndpoint(t *testing.T) {
	mux, st := newUsageServer(t)
	now := time.Now()
	st.Add(usage.Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 4, Output: 1, Total: 5})

	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		Total usage.Totals `json:"total"`
		Today usage.Totals `json:"today"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total.Requests != 1 || out.Total.Total != 5 || out.Today.Total != 5 {
		t.Fatalf("概览用量错误：%+v / %+v", out.Total, out.Today)
	}
}
