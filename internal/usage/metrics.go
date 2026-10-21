package usage

// Metrics 是一组合计的派生指标。指针字段为空表示没有足够样本，而不是 0。
type Metrics struct {
	Requests      int      `json:"requests"`
	OK            int      `json:"ok"`
	Failed        int      `json:"failed"`
	Cancelled     int      `json:"cancelled"`
	Excluded      int      `json:"excluded"`
	SuccessRate   *float64 `json:"success_rate"`
	FailureRate   *float64 `json:"failure_rate"`
	TPS           *float64 `json:"tps"`
	TTFTMS        *float64 `json:"ttft_ms"`
	SpeedSamples  int      `json:"speed_samples"`
	SpeedCoverage *float64 `json:"speed_coverage"`
}

// Derive 计算可用性、生成速度与首 token 延迟。
// 供应商失败进入成功率分母；客户端取消与本地错误只计入 excluded。
// 旧版本数据只有 Err 没有分类字段时，未分类的 Err 按供应商失败处理。
func (t Totals) Derive() Metrics {
	failed := t.UpstreamErr + t.AuthErr
	knownErr := t.UpstreamErr + t.AuthErr + t.ClientCancel + t.ClientErr
	if legacy := t.Err - knownErr; legacy > 0 {
		failed += legacy
	}
	cancelled := t.ClientCancel
	excluded := t.ClientCancel + t.ClientErr
	total := t.OK + failed

	out := Metrics{
		Requests:     t.Requests,
		OK:           t.OK,
		Failed:       failed,
		Cancelled:    cancelled,
		Excluded:     excluded,
		SpeedSamples: t.SpeedSamples,
	}
	if total > 0 {
		rate := float64(t.OK) / float64(total)
		failure := 1 - rate
		out.SuccessRate = &rate
		out.FailureRate = &failure
	}
	if t.SpeedSamples > 0 && t.DecodeMS > 0 {
		tps := float64(t.SpeedOutput) * 1000 / float64(t.DecodeMS)
		out.TPS = &tps
	}
	if t.TTFTSamples > 0 {
		ttft := float64(t.TTFTMS) / float64(t.TTFTSamples)
		out.TTFTMS = &ttft
	}
	if t.Requests > 0 {
		coverage := float64(t.SpeedSamples) / float64(t.Requests)
		out.SpeedCoverage = &coverage
	}
	return out
}
