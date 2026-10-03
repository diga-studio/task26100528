// Package risktrend 提供单部门钓鱼识别能力分的风险趋势统计。
// 数据保存在进程内存中，由上层报表直接调用，调用按单线程顺序执行。
package risktrend

import (
	"errors"
	"fmt"
	"sort"
)

// RangeDays 是统计范围的天数（自统计起点起）。
const RangeDays = 365

// highRiskThreshold 是部门能力分高风险阈值（严格低于该值视为高风险）。
const highRiskThreshold = 50

const secondsPerDay = 86400

// Employee 描述一名在统计范围内固定不变的员工。
type Employee struct {
	ID      string // 员工唯一标识
	Weight  int    // 固定权重，取值 1..100
	Initial int    // 统计起点的能力分，取值 0..100
}

// Assessment 描述一条测评记录，写入时按（员工， 生效时间）覆盖。
type Assessment struct {
	EmployeeID string // 员工标识
	Time       int64  // 生效时间，整数 UTC 秒，须在 [起点， 起点+365天) 内
	Score      int    // 能力分，取值 0..100
}

// Stats 是部门风险趋势统计器。仅支持单线程顺序调用。
type Stats struct {
	start       int64
	end         int64 // 不含
	totalWeight int64
	emps        map[string]*employeeState
	idx         *eventIndex
}

// New 创建统计器。start 为统计起点（整数 UTC 秒），统计范围为 [start, start+365天)。
// employees 为统计范围内固定不变的员工名单及其权重、起点能力分。
// history 为可选的已有测评，按任意顺序提供；同员工同时间的多条记录以后出现者为准。
func New(start int64, employees []Employee, history []Assessment) (*Stats, error) {
	if len(employees) == 0 {
		return nil, errors.New("risktrend: employee list is empty")
	}
	s := &Stats{
		start: start,
		end:   start + RangeDays*secondsPerDay,
		emps:  make(map[string]*employeeState, len(employees)),
	}
	for i, e := range employees {
		if e.ID == "" {
			return nil, errors.New("risktrend: employee ID is empty")
		}
		if _, dup := s.emps[e.ID]; dup {
			return nil, fmt.Errorf("risktrend: duplicate employee ID %q", e.ID)
		}
		if e.Weight < 1 || e.Weight > 100 {
			return nil, fmt.Errorf("risktrend: employee %q weight %d out of range [1,100]", e.ID, e.Weight)
		}
		if e.Initial < 0 || e.Initial > 100 {
			return nil, fmt.Errorf("risktrend: employee %q initial score %d out of range [0,100]", e.ID, e.Initial)
		}
		seed := uint64(i+1)*0x9e3779b97f4a7c15 | 1
		s.emps[e.ID] = &employeeState{
			weight:  int64(e.Weight),
			initial: e.Initial,
			records: recordTreap{rng: seed},
		}
		s.totalWeight += int64(e.Weight)
	}

	// 按员工分组历史测评，逐员工按时间排序（同时间后出现的覆盖先出现的），
	// 同时构建员工记录树与全局差分事件。
	perEmp := make(map[string][]Assessment, len(s.emps))
	for _, a := range history {
		if _, ok := s.emps[a.EmployeeID]; !ok {
			return nil, fmt.Errorf("risktrend: history references unknown employee %q", a.EmployeeID)
		}
		if err := s.validateAssessment(a); err != nil {
			return nil, err
		}
		perEmp[a.EmployeeID] = append(perEmp[a.EmployeeID], a)
	}
	type pair struct {
		t, d int64
	}
	pairs := make([]pair, 0, len(history))
	var base int64
	for id, st := range s.emps {
		base += st.weight * int64(st.initial)
		recs := perEmp[id]
		if len(recs) == 0 {
			continue
		}
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].Time < recs[j].Time })
		prev := st.initial
		// 去重：同时间保留最后出现的分数，并重建成树。
		dedup := recs[:0]
		for _, a := range recs {
			if n := len(dedup); n > 0 && dedup[n-1].Time == a.Time {
				dedup[n-1].Score = a.Score
			} else {
				dedup = append(dedup, a)
			}
		}
		st.records = recordTreap{rng: st.records.rng}
		for _, a := range dedup {
			st.records.insert(a.Time, a.Score)
			d := st.weight * int64(a.Score-prev)
			if d != 0 {
				pairs = append(pairs, pair{a.Time, d})
			}
			prev = a.Score
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].t < pairs[j].t })
	times := make([]int64, 0, len(pairs))
	deltas := make([]int64, 0, len(pairs))
	for _, p := range pairs {
		if n := len(times); n > 0 && times[n-1] == p.t {
			deltas[n-1] += p.d
		} else {
			times = append(times, p.t)
			deltas = append(deltas, p.d)
		}
	}
	s.idx = newEventIndex(s.start, s.end, base, times, deltas)
	return s, nil
}

func (s *Stats) validateAssessment(a Assessment) error {
	if a.Score < 0 || a.Score > 100 {
		return fmt.Errorf("risktrend: score %d out of range [0,100]", a.Score)
	}
	if a.Time < s.start || a.Time >= s.end {
		return fmt.Errorf("risktrend: assessment time %d out of statistics range [%d, %d)", a.Time, s.start, s.end)
	}
	return nil
}

// AddAssessment 补录或覆盖一条测评。同员工同生效时间的再次写入覆盖原分数，其他记录不变。
// 写入返回后，后续查询（包括已查询过的历史区间）立即反映更新。
func (s *Stats) AddAssessment(a Assessment) error {
	st, ok := s.emps[a.EmployeeID]
	if !ok {
		return fmt.Errorf("risktrend: unknown employee %q", a.EmployeeID)
	}
	if err := s.validateAssessment(a); err != nil {
		return err
	}
	atScore, hasAt, predScore, hasPred, succTime, hasSucc := st.records.lookup(a.Time)
	var old int
	switch {
	case hasAt:
		old = atScore
		if atScore == a.Score {
			return nil // 重复写入相同内容，结果不变
		}
		st.records.set(a.Time, a.Score)
	case hasPred:
		old = predScore
		st.records.insert(a.Time, a.Score)
	default:
		old = st.initial
		st.records.insert(a.Time, a.Score)
	}
	// 该员工在 [a.Time, 下一条测评) 内的有效分由 old 变为 a.Score，
	// 对部门加权总分等价于在该区间上加常数 w*(new-old)，即两个端点差分。
	delta := st.weight * int64(a.Score-old)
	s.idx.addEvent(a.Time, delta)
	if hasSucc {
		s.idx.addEvent(succTime, -delta)
	}
	return nil
}

// Query 查询左闭右开区间 [from, to)（自动裁剪到统计范围内）。
// 返回区间内按持续时间加权的部门平均能力分，以及部门能力分严格低于 50 的累计秒数。
// 区间无效（裁剪后为空）时返回 (0, 0)。
func (s *Stats) Query(from, to int64) (avgScore float64, highRiskSeconds int64) {
	if from < s.start {
		from = s.start
	}
	if to > s.end {
		to = s.end
	}
	if from >= to {
		return 0, 0
	}
	integral, risk := s.idx.query(from, to, highRiskThreshold*s.totalWeight)
	avg := float64(integral) / (float64(s.totalWeight) * float64(to-from))
	return avg, risk
}
