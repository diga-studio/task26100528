package risk

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// brute 是独立实现的参照：保存全部记录，每次查询从原始记录重算。
type brute struct {
	start, end int64
	ids        []string
	weight     map[string]int64
	startScore map[string]int
	events     map[string]map[int64]int
	totalW     int64
}

func newBrute(start int64, emps []Employee) *brute {
	b := &brute{
		start:      start,
		end:        start + 365*24*3600,
		weight:     map[string]int64{},
		startScore: map[string]int{},
		events:     map[string]map[int64]int{},
	}
	for _, e := range emps {
		b.ids = append(b.ids, e.ID)
		b.weight[e.ID] = int64(e.Weight)
		b.startScore[e.ID] = e.StartScore
		b.events[e.ID] = map[int64]int{}
		b.totalW += int64(e.Weight)
	}
	return b
}

func (b *brute) upsert(a Assessment) {
	b.events[a.EmployeeID][a.Time] = a.Score
}

func (b *brute) effAt(id string, t int64) int {
	best := b.startScore[id]
	var bestT int64 = -1
	for ts, s := range b.events[id] {
		if ts <= t && ts > bestT {
			bestT = ts
			best = s
		}
	}
	return best
}

func (b *brute) query(from, to int64) Stats {
	if from < b.start {
		from = b.start
	}
	if to > b.end {
		to = b.end
	}
	if from >= to {
		return Stats{}
	}
	cuts := []int64{from, to}
	for _, evs := range b.events {
		for ts := range evs {
			if ts > from && ts < to {
				cuts = append(cuts, ts)
			}
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	var integral, risk int64
	for i := 0; i+1 < len(cuts); i++ {
		s, e := cuts[i], cuts[i+1]
		if s >= e {
			continue
		}
		var sum int64
		for _, id := range b.ids {
			sum += b.weight[id] * int64(b.effAt(id, s))
		}
		d := e - s
		integral += sum * d
		if sum < 50*b.totalW {
			risk += d
		}
	}
	return Stats{
		Average:         float64(integral) / (float64(b.totalW) * float64(to-from)),
		HighRiskSeconds: risk,
	}
}

func checkQuery(t *testing.T, tr *Tracker, b *brute, from, to int64) {
	t.Helper()
	got := tr.Query(from, to)
	want := b.query(from, to)
	if math.Abs(got.Average-want.Average) > 1e-9 {
		t.Errorf("区间[%d,%d) 平均分: got %v want %v", from, to, got.Average, want.Average)
	}
	if got.HighRiskSeconds != want.HighRiskSeconds {
		t.Errorf("区间[%d,%d) 高风险秒数: got %d want %d", from, to, got.HighRiskSeconds, want.HighRiskSeconds)
	}
}

// 非均匀权重、不同员工同秒变分、部门分恰好等于 50。
func TestWeightedSameSecondAndExactBoundary(t *testing.T) {
	start := int64(1_700_000_000)
	emps := []Employee{
		{ID: "a", Weight: 1, StartScore: 20},
		{ID: "b", Weight: 3, StartScore: 60},
		{ID: "c", Weight: 6, StartScore: 90},
	}
	tr, err := NewTracker(start, emps, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 初始: S = 1*20+3*60+6*90 = 740, W=10, 平均 74。
	if got := tr.Query(start, start+100); got.Average != 74 || got.HighRiskSeconds != 0 {
		t.Fatalf("初始状态错误: %+v", got)
	}
	// 同秒变分: a->50, c->40。S = 50+180+240 = 470 < 500, 高风险。
	ts := start + 1000
	if err := tr.Upsert(Assessment{EmployeeID: "a", Time: ts, Score: 50}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Upsert(Assessment{EmployeeID: "c", Time: ts, Score: 40}); err != nil {
		t.Fatal(err)
	}
	got := tr.Query(ts, ts+500)
	if got.Average != 47 || got.HighRiskSeconds != 500 {
		t.Fatalf("同秒变分错误: %+v", got)
	}
	// 恰好等于 50: b->40 使 S = 50+120+240 = 410? 改为精确构造:
	// 令 c->50: S = 50+180+300 = 530。令 a->20,b->20,c->70: S=20+60+420=500 恰为 50。
	if err := tr.Upsert(Assessment{EmployeeID: "a", Time: ts, Score: 20}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Upsert(Assessment{EmployeeID: "b", Time: ts, Score: 20}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Upsert(Assessment{EmployeeID: "c", Time: ts, Score: 70}); err != nil {
		t.Fatal(err)
	}
	got = tr.Query(ts, ts+500)
	if got.Average != 50 {
		t.Fatalf("期望平均恰好 50, got %v", got.Average)
	}
	if got.HighRiskSeconds != 0 {
		t.Fatalf("恰好等于 50 不应计入高风险, got %d", got.HighRiskSeconds)
	}
	// 子区间与整段风险状态独立: 前半段低风险后半段高风险。
	if err := tr.Upsert(Assessment{EmployeeID: "c", Time: ts + 250, Score: 0}); err != nil {
		t.Fatal(err)
	}
	// [ts, ts+250): S=500 非风险; [ts+250, ts+500): S=20+60+0=80 风险。
	got = tr.Query(ts, ts+500)
	if got.HighRiskSeconds != 250 {
		t.Fatalf("分段风险累计错误: %+v", got)
	}
	if got.Average != (50.0*250+8.0*250)/500 {
		t.Fatalf("分段平均错误: %+v", got)
	}
}

// 先录较晚测评，再补录较早测评并覆盖；受影响区间正确变化，
// 下一条测评之后保持不变；重复写入相同内容不改变结果。
func TestBackfillOverwriteAndIdempotent(t *testing.T) {
	start := int64(1_700_000_000)
	emps := []Employee{{ID: "a", Weight: 2, StartScore: 100}}
	tr, err := NewTracker(start, emps, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := newBrute(start, emps)
	// 先录较晚: t=1000 得 0 分。
	late := Assessment{EmployeeID: "a", Time: start + 1000, Score: 0}
	tr.Upsert(late)
	b.upsert(late)
	before := tr.Query(start+1000, start+2000)
	// 补录较早: t=500 得 80 分。
	early := Assessment{EmployeeID: "a", Time: start + 500, Score: 80}
	tr.Upsert(early)
	b.upsert(early)
	// [500,1000) 应变 80，[1000,2000) 不变。
	checkQuery(t, tr, b, start+500, start+1000)
	if after := tr.Query(start+1000, start+2000); after != before {
		t.Fatalf("补录不应影响下一条测评之后: before %+v after %+v", before, after)
	}
	// 覆盖补录的分数: 80 -> 30（高风险）。
	early.Score = 30
	tr.Upsert(early)
	b.upsert(early)
	checkQuery(t, tr, b, start+500, start+1000)
	checkQuery(t, tr, b, start, start+2000)
	// 重复写入相同内容不改变结果。
	snap := tr.Query(start, start+2000)
	tr.Upsert(early)
	if got := tr.Query(start, start+2000); got != snap {
		t.Fatalf("重复写入改变了结果: %+v -> %+v", snap, got)
	}
	checkQuery(t, tr, b, start, start+2000)
}

// 随机操作与暴力重算交叉验证。
func TestRandomCrossCheck(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	start := int64(1_700_000_000)
	end := start + 365*24*3600
	emps := make([]Employee, 12)
	for i := range emps {
		emps[i] = Employee{
			ID:         string(rune('a' + i)),
			Weight:     1 + rnd.Intn(100),
			StartScore: rnd.Intn(101),
		}
	}
	var initial []Assessment
	for i := 0; i < 300; i++ {
		initial = append(initial, Assessment{
			EmployeeID: emps[rnd.Intn(len(emps))].ID,
			Time:       start + rnd.Int63n(end-start),
			Score:      rnd.Intn(101),
		})
	}
	tr, err := NewTracker(start, emps, initial)
	if err != nil {
		t.Fatal(err)
	}
	b := newBrute(start, emps)
	for _, a := range initial {
		b.upsert(a)
	}
	for i := 0; i < 400; i++ {
		a := Assessment{
			EmployeeID: emps[rnd.Intn(len(emps))].ID,
			Time:       start + rnd.Int63n(end-start),
			Score:      rnd.Intn(101),
		}
		if err := tr.Upsert(a); err != nil {
			t.Fatal(err)
		}
		b.upsert(a)
		from := start + rnd.Int63n(end-start)
		to := from + rnd.Int63n(end-from)
		checkQuery(t, tr, b, from, to)
	}
	// 整段查询。
	checkQuery(t, tr, b, start, end)
}

// 初始装载含同员工同时间的重复记录时以靠后为准。
func TestInitialDuplicateLastWins(t *testing.T) {
	start := int64(1_700_000_000)
	emps := []Employee{{ID: "a", Weight: 1, StartScore: 10}}
	initial := []Assessment{
		{EmployeeID: "a", Time: start + 100, Score: 50},
		{EmployeeID: "a", Time: start + 100, Score: 90},
	}
	tr, err := NewTracker(start, emps, initial)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Query(start+100, start+200); got.Average != 90 {
		t.Fatalf("重复记录应以靠后为准, got %+v", got)
	}
}

// 参数校验。
func TestValidation(t *testing.T) {
	start := int64(1_700_000_000)
	if _, err := NewTracker(start, nil, nil); err == nil {
		t.Fatal("空员工名单应报错")
	}
	if _, err := NewTracker(start, []Employee{{ID: "a", Weight: 0, StartScore: 1}}, nil); err == nil {
		t.Fatal("非法权重应报错")
	}
	if _, err := NewTracker(start, []Employee{{ID: "a", Weight: 1, StartScore: 101}}, nil); err == nil {
		t.Fatal("非法起点分应报错")
	}
	if _, err := NewTracker(start, []Employee{{ID: "a", Weight: 1, StartScore: 1}, {ID: "a", Weight: 1, StartScore: 2}}, nil); err == nil {
		t.Fatal("重复员工应报错")
	}
	tr, err := NewTracker(start, []Employee{{ID: "a", Weight: 1, StartScore: 50}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Upsert(Assessment{EmployeeID: "x", Time: start, Score: 1}); err == nil {
		t.Fatal("未知员工应报错")
	}
	if err := tr.Upsert(Assessment{EmployeeID: "a", Time: start - 1, Score: 1}); err == nil {
		t.Fatal("范围外时间应报错")
	}
	if err := tr.Upsert(Assessment{EmployeeID: "a", Time: start, Score: 200}); err == nil {
		t.Fatal("非法分数应报错")
	}
}
