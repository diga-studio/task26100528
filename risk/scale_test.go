package risk

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"
)

const (
	scaleEmp     = 500
	scaleEvents  = 1_000_000
	scaleWrites  = 2_000
	scaleQueries = 10_000
)

// 规模与性能验收：500 名员工、100 万条初始测评；
// 交错 1 万次查询与 2000 次补录/覆盖，总耗时不超过 15 秒，
// 堆内存不超过 256 MiB。事件时间非均匀分布（70% 集中在前 30 天），
// 少数员工全年只有起点分数并被反复补录覆盖，影响延续到年末。
func TestScalePerf(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过规模测试")
	}
	rnd := rand.New(rand.NewSource(7))
	start := int64(1_700_000_000)
	end := start + 365*24*3600
	first30d := int64(30 * 24 * 3600)

	emps := make([]Employee, scaleEmp)
	for i := range emps {
		emps[i] = Employee{
			ID:         "emp" + string(rune(i/100+'0')) + string(rune(i/10%10+'0')) + string(rune(i%10+'0')),
			Weight:     1 + rnd.Intn(100),
			StartScore: rnd.Intn(101),
		}
	}
	// 前 10 名员工全年只有起点分数（稀疏），其余产生密集历史。
	sparse := emps[:10]
	// 事件时间非均匀：70% 落在前 30 天。
	genTime := func() int64 {
		if rnd.Float64() < 0.7 {
			return start + rnd.Int63n(first30d)
		}
		return start + first30d + rnd.Int63n(365*24*3600-first30d)
	}
	initial := make([]Assessment, 0, scaleEvents)
	for i := 0; i < scaleEvents; i++ {
		e := emps[10+rnd.Intn(scaleEmp-10)]
		initial = append(initial, Assessment{EmployeeID: e.ID, Time: genTime(), Score: rnd.Intn(101)})
	}
	tr, err := NewTracker(start, emps, initial)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("装载后堆内存: %.1f MiB", float64(m.HeapAlloc)/1024/1024)
	if limit := uint64(256) << 20; m.HeapAlloc > limit {
		t.Fatalf("堆内存超限: %.1f MiB > 256 MiB", float64(m.HeapAlloc)/1024/1024)
	}

	// 交错执行：每次迭代 5 个查询 + 1 次写入。
	t0 := time.Now()
	for i := 0; i < scaleWrites; i++ {
		for j := 0; j < scaleQueries/scaleWrites; j++ {
			span := int64(90*24*3600) + rnd.Int63n(int64(275*24*3600))
			from := start + rnd.Int63n(365*24*3600-span)
			st := tr.Query(from, from+span)
			if st.Average < 0 || st.Average > 100 {
				t.Fatalf("平均分越界: %v", st.Average)
			}
			if st.HighRiskSeconds < 0 || st.HighRiskSeconds > span {
				t.Fatalf("高风险秒数越界: %d", st.HighRiskSeconds)
			}
		}
		var a Assessment
		if i%2 == 0 {
			// 一半写入：稀疏员工年初补录/反复覆盖，影响延续到年末。
			e := sparse[rnd.Intn(len(sparse))]
			a = Assessment{EmployeeID: e.ID, Time: start + rnd.Int63n(first30d), Score: rnd.Intn(101)}
		} else {
			// 另一半写入：前 30 天内的随机补录/覆盖。
			e := emps[rnd.Intn(scaleEmp)]
			a = Assessment{EmployeeID: e.ID, Time: start + rnd.Int63n(first30d), Score: rnd.Intn(101)}
		}
		if err := tr.Upsert(a); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(t0)
	t.Logf("交错阶段耗时: %v", elapsed)
	if elapsed > 15*time.Second {
		t.Fatalf("耗时超限: %v > 15s", elapsed)
	}
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("结束后堆内存: %.1f MiB", float64(m.HeapAlloc)/1024/1024)
	if limit := uint64(256) << 20; m.HeapAlloc > limit {
		t.Fatalf("堆内存超限: %.1f MiB > 256 MiB", float64(m.HeapAlloc)/1024/1024)
	}

	// 长区间查询准确性：重建只含 initial 的统计器，
	// 用按员工前缀积分的独立方法重算平均分对照。
	tr2, err := NewTracker(start, emps, initial)
	if err != nil {
		t.Fatal(err)
	}
	// 每名员工的事件按时间排序，同秒记录以靠后为准（与库语义一致）。
	perEmp := make(map[string][]Assessment, scaleEmp)
	for _, a := range initial {
		perEmp[a.EmployeeID] = append(perEmp[a.EmployeeID], a)
	}
	for id, evs := range perEmp {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time < evs[j].Time })
		dedup := evs[:0]
		for _, a := range evs {
			if n := len(dedup); n > 0 && dedup[n-1].Time == a.Time {
				dedup[n-1] = a
			} else {
				dedup = append(dedup, a)
			}
		}
		perEmp[id] = dedup
	}
	var totalW int64
	for _, e := range emps {
		totalW += int64(e.Weight)
	}
	for _, span := range []int64{90 * 24 * 3600, 180 * 24 * 3600, 365 * 24 * 3600} {
		from := start
		to := from + span
		if to > end {
			to = end
		}
		var num int64
		for _, e := range emps {
			var integ, cur int64
			score := int64(e.StartScore)
			cur = from
			for _, a := range perEmp[e.ID] {
				if a.Time <= from {
					score = int64(a.Score)
					continue
				}
				if a.Time >= to {
					break
				}
				integ += score * (a.Time - cur)
				score = int64(a.Score)
				cur = a.Time
			}
			integ += score * (to - cur)
			num += int64(e.Weight) * integ
		}
		want := float64(num) / (float64(totalW) * float64(to-from))
		if got := tr2.Query(from, to).Average; math.Abs(got-want) > 1e-9 {
			t.Fatalf("跨度%d天平均分不一致: got %v want %v", span/86400, got, want)
		}
	}
}
