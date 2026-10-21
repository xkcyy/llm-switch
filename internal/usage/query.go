package usage

import (
	"sort"
	"time"
)

// 时间粒度。
const (
	GranMinute = "minute"
	GranHour   = "hour"
	GranDay    = "day"
	GranMonth  = "month"
)

// 分组维度。
const (
	GroupAll      = "all"
	GroupModel    = "model"
	GroupProvider = "provider"
	GroupCaller   = "caller"
)

// Query 是一次用量查询。From / To 都按粒度取整（小时粒度给整点，日粒度给当天零点）。
type Query struct {
	From, To    time.Time
	Granularity string
	Group       string
	ProviderID  string
	ModelID     string
	Caller      string
}

// SeriesPoint 是时间序列上的一个点。Group 是该点所属的分组键。
type SeriesPoint struct {
	Period string `json:"period"`
	Group  string `json:"group,omitempty"`
	Totals
}

// GroupRow 是一个分组的合计。名称字段是该组最新时段写入时的快照。
type GroupRow struct {
	Key           string `json:"key"`
	ProviderID    string `json:"provider_id,omitempty"`
	ProviderName  string `json:"provider_name,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	ModelName     string `json:"model_name,omitempty"`
	Caller        string `json:"caller,omitempty"`
	LastPeriod    string `json:"last_period,omitempty"`
	LastOKPeriod  string `json:"last_ok_period,omitempty"`
	LastErrPeriod string `json:"last_err_period,omitempty"`
	Totals

	// namePeriod 记住名称快照来自哪个时段，用于取最新的一份（不参与序列化）。
	namePeriod string
}

// Result 是一次查询的完整结果。
type Result struct {
	Totals Totals        `json:"totals"`
	Series []SeriesPoint `json:"series"`
	Groups []GroupRow    `json:"groups"`
}

// EarliestDay 返回日桶中最早的一天，供“全部”范围查询使用。
func (s *Store) EarliestDay() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	earliest := ""
	for k := range s.days {
		if earliest == "" || k.Period < earliest {
			earliest = k.Period
		}
	}
	return earliest, earliest != ""
}

// Query 汇总指定区间与维度的用量。月粒度由日桶合并而来，小时/分钟粒度读对应桶。
func (s *Store) Query(q Query) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	source := s.days
	from, to := q.From.Format(dayLayout), q.To.Format(dayLayout)
	switch q.Granularity {
	case GranMinute:
		source = s.minutes
		from, to = q.From.Format(minuteLayout), q.To.Format(minuteLayout)
	case GranHour:
		source = s.hours
		from, to = q.From.Format(hourLayout), q.To.Format(hourLayout)
	}

	series := map[[2]string]*Totals{}
	groups := map[string]*GroupRow{}
	var tot Totals
	for k, b := range source {
		if k.Period < from || k.Period > to {
			continue
		}
		if q.ProviderID != "" && k.ProviderID != q.ProviderID {
			continue
		}
		if q.ModelID != "" && k.ModelID != q.ModelID {
			continue
		}
		if q.Caller != "" && k.Caller != q.Caller {
			continue
		}
		row := groupFor(groups, q.Group, k)
		p := k.Period
		if q.Granularity == GranMonth && len(p) >= 7 {
			p = p[:7]
		}
		key := [2]string{p, row.Key}
		point := series[key]
		if point == nil {
			point = &Totals{}
			series[key] = point
		}
		point.add(&b.Totals)
		tot.add(&b.Totals)
		row.add(&b.Totals)
		if k.Period > row.LastPeriod {
			row.LastPeriod = k.Period
		}
		if b.OK > 0 && k.Period > row.LastOKPeriod {
			row.LastOKPeriod = k.Period
		}
		if b.Err > 0 && k.Period > row.LastErrPeriod {
			row.LastErrPeriod = k.Period
		}
		if k.Period >= row.namePeriod {
			row.namePeriod = k.Period
			if b.ProviderName != "" {
				row.ProviderName = b.ProviderName
			}
			if b.ModelName != "" {
				row.ModelName = b.ModelName
			}
		}
	}

	out := Result{Totals: tot}
	for key, b := range series {
		out.Series = append(out.Series, SeriesPoint{Period: key[0], Group: key[1], Totals: *b})
	}
	sort.Slice(out.Series, func(i, j int) bool {
		if out.Series[i].Period != out.Series[j].Period {
			return out.Series[i].Period < out.Series[j].Period
		}
		return out.Series[i].Group < out.Series[j].Group
	})

	for _, row := range groups {
		out.Groups = append(out.Groups, *row)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].Total != out.Groups[j].Total {
			return out.Groups[i].Total > out.Groups[j].Total
		}
		return out.Groups[i].Requests > out.Groups[j].Requests
	})
	return out
}

func groupFor(m map[string]*GroupRow, group string, k bucketKey) *GroupRow {
	var key, pid, mid, caller string
	switch group {
	case GroupAll:
		// 汇总：所有桶并为一行、一条序列
	case GroupProvider:
		pid, key = k.ProviderID, k.ProviderID
	case GroupCaller:
		caller = k.Caller
		if caller == "" {
			caller = "未知"
		}
		key = caller
	default: // 模型：不同供应商下的同名模型必须分开
		pid, mid = k.ProviderID, k.ModelID
		key = k.ProviderID + "/" + k.ModelID
	}
	row, ok := m[key]
	if !ok {
		row = &GroupRow{Key: key, ProviderID: pid, ModelID: mid, Caller: caller}
		m[key] = row
	}
	return row
}
