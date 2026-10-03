package risktrend

import "testing"

// 先录入较晚测评，再补录较早测评并覆盖其分数：
// 受影响区间正确变化，下一条测评之后的结果保持不变，重复写入相同内容不改变结果。
func TestBackfillAndOverwrite(t *testing.T) {
	emps := []Employee{
		{ID: "a", Weight: 2, Initial: 70},
		{ID: "b", Weight: 1, Initial: 30},
	}
	tEarly := testStart + 10*secondsPerDay
	tLate := testStart + 200*secondsPerDay
	end := testStart + RangeDays*secondsPerDay

	var recs []Assessment
	s, err := New(testStart, emps, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	write := func(a Assessment) {
		t.Helper()
		if err := s.AddAssessment(a); err != nil {
			t.Fatalf("AddAssessment(%+v): %v", a, err)
		}
		recs = append(recs, a)
	}
	checkAll := func() {
		t.Helper()
		tls := buildRef(emps, recs)
		for _, iv := range [][2]int64{
			{testStart, end},
			{testStart, tEarly},
			{tEarly, tLate},
			{tLate, end},
			{tEarly + 1, tLate - 1},
			{testStart, tLate},
		} {
			checkQuery(t, s, tls, iv[0], iv[1])
		}
	}

	// 1. 先录入较晚测评。
	write(Assessment{EmployeeID: "a", Time: tLate, Score: 10})
	checkAll()
	// 下一条测评之后的基准结果（用于验证补录不影响更晚区间）。
	avgAfter, riskAfter := s.Query(tLate, end)

	// 2. 补录较早测评：[tEarly, tLate) 受影响，[tLate, end) 不变。
	write(Assessment{EmployeeID: "a", Time: tEarly, Score: 90})
	checkAll()
	if a2, r2 := s.Query(tLate, end); a2 != avgAfter || r2 != riskAfter {
		t.Errorf("after backfill, [tLate,end) = (%v, %d), want unchanged (%v, %d)", a2, r2, avgAfter, riskAfter)
	}

	// 3. 覆盖补录的分数。
	write(Assessment{EmployeeID: "a", Time: tEarly, Score: 20})
	checkAll()
	if a2, r2 := s.Query(tLate, end); a2 != avgAfter || r2 != riskAfter {
		t.Errorf("after overwrite, [tLate,end) = (%v, %d), want unchanged (%v, %d)", a2, r2, avgAfter, riskAfter)
	}

	// 4. 重复写入相同内容：结果不变。
	avgMid1, riskMid1 := s.Query(tEarly, tLate)
	avgAll1, riskAll1 := s.Query(testStart, end)
	write(Assessment{EmployeeID: "a", Time: tEarly, Score: 20})
	if a2, r2 := s.Query(tEarly, tLate); a2 != avgMid1 || r2 != riskMid1 {
		t.Errorf("duplicate write changed [tEarly,tLate): (%v, %d) -> (%v, %d)", avgMid1, riskMid1, a2, r2)
	}
	if a2, r2 := s.Query(testStart, end); a2 != avgAll1 || r2 != riskAll1 {
		t.Errorf("duplicate write changed full range: (%v, %d) -> (%v, %d)", avgAll1, riskAll1, a2, r2)
	}

	// 5. 在两条记录之间再补录一条，随后覆盖；每步都与独立重算一致。
	tMid := testStart + 100*secondsPerDay
	write(Assessment{EmployeeID: "a", Time: tMid, Score: 55})
	checkAll()
	write(Assessment{EmployeeID: "a", Time: tMid, Score: 45})
	checkAll()
	write(Assessment{EmployeeID: "b", Time: tEarly, Score: 80})
	checkAll()
}
