package risktrend

import (
	"math"
	"testing"
)

const testStart = int64(1704067200) // 2024-01-01T00:00:00Z

func almostEqual(a, b float64) bool { return math.Abs(a-b) <= 1e-6 }

func checkQuery(t *testing.T, s *Stats, tls []refTimeline, from, to int64) {
	t.Helper()
	wantAvg, wantRisk := refQuery(tls, from, to)
	gotAvg, gotRisk := s.Query(from, to)
	if gotRisk != wantRisk {
		t.Errorf("Query(%d, %d) risk = %d, want %d", from, to, gotRisk, wantRisk)
	}
	if !almostEqual(gotAvg, wantAvg) {
		t.Errorf("Query(%d, %d) avg = %v, want %v", from, to, gotAvg, wantAvg)
	}
}

// 非均匀权重、不同员工同秒变分、部门分恰好等于 50 不计入高风险；
// 多个查询区间与独立重算一致；区间平均分不低于 50 不代表整段无风险。
func TestWeightsSameSecondAndBoundary50(t *testing.T) {
	emps := []Employee{
		{ID: "a", Weight: 1, Initial: 40},
		{ID: "b", Weight: 3, Initial: 60},
		{ID: "c", Weight: 2, Initial: 50},
	}
	// 总权重 6，恰好 50 分 ⟺ 加权总分恰好 300。
	t1 := testStart + 10*secondsPerDay
	t2 := testStart + 20*secondsPerDay
	t3 := testStart + 30*secondsPerDay
	history := []Assessment{
		// 同一秒两名员工变分：V = 100*1 + 0*3 + 50*2 = 200，avg ≈ 33.3 < 50。
		{EmployeeID: "a", Time: t1, Score: 100},
		{EmployeeID: "b", Time: t1, Score: 0},
		// V = 50 + 150 + 100 = 300，avg 恰好 50，不计高风险。
		{EmployeeID: "a", Time: t2, Score: 50},
		{EmployeeID: "b", Time: t2, Score: 50},
		// V = 0 + 300 + 100 = 400，avg ≈ 66.7。
		{EmployeeID: "a", Time: t3, Score: 0},
		{EmployeeID: "b", Time: t3, Score: 100},
	}
	s, err := New(testStart, emps, history)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tls := buildRef(emps, history)

	// 恰好 50 的区间 [t2, t3)：高风险秒数必须为 0。
	if _, risk := s.Query(t2, t3); risk != 0 {
		t.Errorf("avg==50 interval risk = %d, want 0", risk)
	}
	// [t1, t3) 区间平均 = (200*10d + 300*10d) / (6*20d) = 41.67 < 50，
	// 但其中 [t2,t3) 并非高风险：不能用区间均值判断整段风险状态。
	avg, risk := s.Query(t1, t3)
	if avg >= 50 || risk != 10*secondsPerDay {
		t.Errorf("Query(t1,t3) = (%v, %d), want avg<50 and risk=%d", avg, risk, 10*secondsPerDay)
	}
	// 反向构造：区间平均 >= 50 但内部存在高风险子段。
	// [t1, t3+10d)：V 为 200/300/400 各 10 天，avg = 50，risk = 10 天。
	avg, risk = s.Query(t1, t3+10*secondsPerDay)
	if !almostEqual(avg, 50) || risk != 10*secondsPerDay {
		t.Errorf("Query(t1,t3+10d) = (%v, %d), want (50, %d)", avg, risk, 10*secondsPerDay)
	}

	// 多个区间与独立重算一致（含跨边界、单秒、全年）。
	intervals := [][2]int64{
		{testStart, testStart + RangeDays*secondsPerDay},
		{testStart, t1},
		{t1 - 1, t1 + 1},
		{t1, t2},
		{t2, t3},
		{t3, testStart + 100*secondsPerDay},
		{t1 + 3600, t3 - 3600},
		{t2, t2 + 1},
	}
	for _, iv := range intervals {
		checkQuery(t, s, tls, iv[0], iv[1])
	}
}

// 查询裁剪到统计范围；无效区间返回 (0, 0)。
func TestQueryClamping(t *testing.T) {
	emps := []Employee{{ID: "a", Weight: 1, Initial: 60}}
	s, err := New(testStart, emps, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	avg, risk := s.Query(testStart-1000, testStart+1000)
	if !almostEqual(avg, 60) || risk != 0 {
		t.Errorf("clamped query = (%v, %d), want (60, 0)", avg, risk)
	}
	if avg, risk := s.Query(testStart+10, testStart+10); avg != 0 || risk != 0 {
		t.Errorf("empty query = (%v, %d), want (0, 0)", avg, risk)
	}
	if avg, risk := s.Query(testStart-100, testStart-50); avg != 0 || risk != 0 {
		t.Errorf("out-of-range query = (%v, %d), want (0, 0)", avg, risk)
	}
}

// 参数校验。
func TestValidation(t *testing.T) {
	good := []Employee{{ID: "a", Weight: 1, Initial: 50}}
	if _, err := New(testStart, nil, nil); err == nil {
		t.Error("empty employees: want error")
	}
	if _, err := New(testStart, []Employee{{ID: "a", Weight: 0, Initial: 50}}, nil); err == nil {
		t.Error("weight 0: want error")
	}
	if _, err := New(testStart, []Employee{{ID: "a", Weight: 101, Initial: 50}}, nil); err == nil {
		t.Error("weight 101: want error")
	}
	if _, err := New(testStart, []Employee{{ID: "a", Weight: 1, Initial: -1}}, nil); err == nil {
		t.Error("initial -1: want error")
	}
	if _, err := New(testStart, []Employee{{ID: "a", Weight: 1, Initial: 50}, {ID: "a", Weight: 1, Initial: 50}}, nil); err == nil {
		t.Error("duplicate ID: want error")
	}
	if _, err := New(testStart, good, []Assessment{{EmployeeID: "x", Time: testStart, Score: 1}}); err == nil {
		t.Error("history unknown employee: want error")
	}
	if _, err := New(testStart, good, []Assessment{{EmployeeID: "a", Time: testStart - 1, Score: 1}}); err == nil {
		t.Error("history time before start: want error")
	}
	s, err := New(testStart, good, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.AddAssessment(Assessment{EmployeeID: "x", Time: testStart, Score: 1}); err == nil {
		t.Error("unknown employee: want error")
	}
	if err := s.AddAssessment(Assessment{EmployeeID: "a", Time: testStart, Score: 101}); err == nil {
		t.Error("score 101: want error")
	}
	if err := s.AddAssessment(Assessment{EmployeeID: "a", Time: testStart + RangeDays*secondsPerDay, Score: 1}); err == nil {
		t.Error("time at range end: want error")
	}
}
