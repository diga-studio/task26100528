package risk

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
)

// windowDays 是统计范围的固定天数。
const windowDays = 365

var (
	errNoEmployees    = errors.New("risk: 员工名单不能为空")
	errDupEmployee    = errors.New("risk: 员工标识重复")
	errBadWeight      = errors.New("risk: 权重必须在 1 至 100 之间")
	errBadScore       = errors.New("risk: 能力分必须在 0 至 100 之间")
	errUnknownEmp     = errors.New("risk: 未知员工")
	errTimeOutOfRange = errors.New("risk: 生效时间不在统计范围内")
)

// Tracker 是部门风险趋势统计器。统计范围固定为 [start, start+365天)，
// 期间员工与权重不变。非并发安全，按单线程顺序调用。
type Tracker struct {
	start       int64
	end         int64
	totalWeight int64
	empIndex    map[string]int
	weights     []int64
	startScores []int
	events      []*eventTreap
	tl          timeline
}

// NewTracker 创建统计器。start 为统计起点（整数 UTC 秒）。
// initial 为已有的测评记录，可按任意顺序给出；
// 同一员工同一生效时间出现多次时以靠后的记录为准。
func NewTracker(start int64, employees []Employee, initial []Assessment) (*Tracker, error) {
	if len(employees) == 0 {
		return nil, errNoEmployees
	}
	t := &Tracker{
		start:       start,
		end:         start + windowDays*24*3600,
		empIndex:    make(map[string]int, len(employees)),
		weights:     make([]int64, len(employees)),
		startScores: make([]int, len(employees)),
		events:      make([]*eventTreap, len(employees)),
	}
	rnd := rand.New(rand.NewSource(1))
	for i, e := range employees {
		if e.Weight < 1 || e.Weight > 100 {
			return nil, fmt.Errorf("%w: %s", errBadWeight, e.ID)
		}
		if e.StartScore < 0 || e.StartScore > 100 {
			return nil, fmt.Errorf("%w: %s", errBadScore, e.ID)
		}
		if _, dup := t.empIndex[e.ID]; dup {
			return nil, fmt.Errorf("%w: %s", errDupEmployee, e.ID)
		}
		t.empIndex[e.ID] = i
		t.weights[i] = int64(e.Weight)
		t.startScores[i] = e.StartScore
		t.totalWeight += int64(e.Weight)
		t.events[i] = newEventTreap(rnd)
	}
	// 校验并登记初始测评。
	type rawEvent struct {
		time  int64
		emp   int
		score int
	}
	raw := make([]rawEvent, 0, len(initial))
	for _, a := range initial {
		idx, err := t.check(a)
		if err != nil {
			return nil, err
		}
		t.events[idx].set(a.Time, a.Score)
	}
	// 从各员工的有序事件表收集去重后的记录，保证与后续写入语义一致。
	for idx, tr := range t.events {
		emp := idx
		tr.forEach(func(ts int64, score int) {
			raw = append(raw, rawEvent{time: ts, emp: emp, score: score})
		})
	}
	// 扫描全部事件，构造 S(t) 的分段常数表示。
	sort.Slice(raw, func(i, j int) bool { return raw[i].time < raw[j].time })
	elems := make([]tlElem, 0, len(raw)+1)
	eff := make([]int, len(employees))
	copy(eff, t.startScores)
	var cur int64
	for i, w := range t.weights {
		cur += w * int64(eff[i])
	}
	prev := t.start
	for i := 0; i < len(raw); {
		j := i
		for j < len(raw) && raw[j].time == raw[i].time {
			j++
		}
		ts := raw[i].time
		if ts > prev {
			elems = append(elems, tlElem{start: prev, dur: ts - prev, val: cur})
		}
		for k := i; k < j; k++ {
			e := raw[k]
			cur += t.weights[e.emp] * int64(e.score-eff[e.emp])
			eff[e.emp] = e.score
		}
		prev = ts
		i = j
	}
	if prev < t.end {
		elems = append(elems, tlElem{start: prev, dur: t.end - prev, val: cur})
	}
	t.tl.build(elems, 50*t.totalWeight)
	return t, nil
}

// check 校验一条测评并返回员工下标。
func (t *Tracker) check(a Assessment) (int, error) {
	idx, ok := t.empIndex[a.EmployeeID]
	if !ok {
		return 0, fmt.Errorf("%w: %s", errUnknownEmp, a.EmployeeID)
	}
	if a.Score < 0 || a.Score > 100 {
		return 0, fmt.Errorf("%w: %s", errBadScore, a.EmployeeID)
	}
	if a.Time < t.start || a.Time >= t.end {
		return 0, fmt.Errorf("%w: %d", errTimeOutOfRange, a.Time)
	}
	return idx, nil
}

// Upsert 补录或覆盖一条测评。同一员工、同一生效时间的再次写入
// 覆盖原分数；返回后所有查询立即反映更新，包括历史区间。
func (t *Tracker) Upsert(a Assessment) error {
	idx, err := t.check(a)
	if err != nil {
		return err
	}
	tr := t.events[idx]
	// 写入前该员工在 a.Time 的有效分数。
	oldEff, ok := tr.get(a.Time)
	if !ok {
		if oldEff, ok = tr.floor(a.Time); !ok {
			oldEff = t.startScores[idx]
		}
	}
	tr.set(a.Time, a.Score)
	next, ok := tr.successor(a.Time)
	if !ok {
		next = t.end
	}
	delta := t.weights[idx] * int64(a.Score-oldEff)
	t.tl.rangeAdd(a.Time, next, delta)
	return nil
}

// Query 查询左闭右开区间 [from, to)，区间会被裁剪到统计范围内；
// 裁剪后为空区间时返回零值。
func (t *Tracker) Query(from, to int64) Stats {
	if from < t.start {
		from = t.start
	}
	if to > t.end {
		to = t.end
	}
	if from >= to {
		return Stats{}
	}
	integral, risk := t.tl.query(from, to)
	return Stats{
		Average:         float64(integral) / (float64(t.totalWeight) * float64(to-from)),
		HighRiskSeconds: risk,
	}
}
