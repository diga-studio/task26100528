package risktrend

import "sort"

// 分块大小：块数约 n/targetBlockSize，查询按块二分、写入按块重建。
const (
	targetBlockSize = 1024
	maxBlockSize    = 2 * targetBlockSize
)

// eventBlock 是差分事件序列的一个块。块内第 i 个事件在 times[i] 生效，
// 其生效后的取值持续 dur[i] 秒；pref 为块内差分前缀和。
// sortedPref/cumDur 是 pref 按值排序后的前缀时长，用于按阈值二分统计。
type eventBlock struct {
	times      []int64
	deltas     []int64
	pref       []int64
	dur        []int64
	sortedPref []int64
	cumDur     []int64
	sumDelta   int64
	sumDur     int64
	sumPrefDur int64 // Σ pref[i]*dur[i]
}

// eventIndex 维护部门加权总分 V(t)（整数）的差分事件序列。
// V(t) = base + Σ_{事件时刻 ≤ t} delta，在 [start, end) 上分段恒定。
// 支持：单点差分更新（区间加常数等价于在两个端点加差分），
// 以及区间查询 V 的时长积分与 V 严格低于阈值的累计秒数。
type eventIndex struct {
	start  int64
	end    int64 // 不含
	base   int64 // 起点处的加权总分
	blocks []*eventBlock
}

// newEventIndex 由已按时间升序聚合的差分事件构建索引；事件时刻须在 [start, end) 内。
func newEventIndex(start, end, base int64, times []int64, deltas []int64) *eventIndex {
	idx := &eventIndex{start: start, end: end, base: base}
	if len(times) == 0 || times[0] > start {
		// 哨兵事件，使事件序列完整覆盖 [start, end)。
		times = append([]int64{start}, times...)
		deltas = append([]int64{0}, deltas...)
	}
	for i := 0; i < len(times); i += targetBlockSize {
		j := i + targetBlockSize
		if j > len(times) {
			j = len(times)
		}
		// 限制容量，避免后续对块内切片 append 时写入相邻块共享的底层数组。
		idx.blocks = append(idx.blocks, &eventBlock{times: times[i:j:j], deltas: deltas[i:j:j]})
	}
	for i, b := range idx.blocks {
		idx.rebuild(b, idx.nextTime(i))
	}
	return idx
}

// nextTime 返回第 i 块最后一个事件的持续终点（下一块首事件时刻或统计终点）。
func (idx *eventIndex) nextTime(i int) int64 {
	if i+1 < len(idx.blocks) {
		return idx.blocks[i+1].times[0]
	}
	return idx.end
}

// rebuild 重算块的派生结构；nextTime 为块末事件的持续终点。
func (idx *eventIndex) rebuild(b *eventBlock, nextTime int64) {
	n := len(b.times)
	b.pref = make([]int64, n)
	b.dur = make([]int64, n)
	b.sortedPref = make([]int64, n)
	b.cumDur = make([]int64, n)
	b.sumDelta = 0
	b.sumDur = 0
	b.sumPrefDur = 0
	var run int64
	for i := 0; i < n; i++ {
		run += b.deltas[i]
		b.pref[i] = run
		end := nextTime
		if i+1 < n {
			end = b.times[i+1]
		}
		b.dur[i] = end - b.times[i]
		b.sortedPref[i] = run
	}
	// 按 pref 升序重排 dur，得到“取值低于某阈值的总时长”的可二分结构。
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, c int) bool { return b.pref[order[a]] < b.pref[order[c]] })
	var cum int64
	for k, i := range order {
		b.sortedPref[k] = b.pref[i]
		cum += b.dur[i]
		b.cumDur[k] = cum
	}
	b.sumDelta = run
	b.sumDur = cum
	for i := 0; i < n; i++ {
		b.sumPrefDur += b.pref[i] * b.dur[i]
	}
}

// findBlock 返回首个事件时刻不大于 tm 的块（blocks[0] 首事件恒为 start ≤ tm）。
func (idx *eventIndex) findBlock(tm int64) int {
	lo, hi := 0, len(idx.blocks)
	for lo+1 < hi {
		mid := int(uint(lo+hi) >> 1)
		if idx.blocks[mid].times[0] <= tm {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// addEvent 在时刻 tm 施加差分 delta（tm 须在 [start, end) 内；delta 为 0 时忽略）。
func (idx *eventIndex) addEvent(tm int64, delta int64) {
	if delta == 0 || tm < idx.start || tm >= idx.end {
		return
	}
	bi := idx.findBlock(tm)
	b := idx.blocks[bi]
	p := sort.Search(len(b.times), func(i int) bool { return b.times[i] >= tm })
	if p < len(b.times) && b.times[p] == tm {
		b.deltas[p] += delta
		idx.rebuild(b, idx.nextTime(bi))
		return
	}
	b.times = append(b.times, 0)
	copy(b.times[p+1:], b.times[p:])
	b.times[p] = tm
	b.deltas = append(b.deltas, 0)
	copy(b.deltas[p+1:], b.deltas[p:])
	b.deltas[p] = delta
	idx.rebuild(b, idx.nextTime(bi))
	if p == 0 && bi > 0 {
		// 块首事件提前，前一块末事件的持续终点改变。
		idx.rebuild(idx.blocks[bi-1], idx.nextTime(bi-1))
	}
	if len(b.times) > maxBlockSize {
		idx.splitBlock(bi)
	}
}

// splitBlock 将第 bi 块均分为二。
func (idx *eventIndex) splitBlock(bi int) {
	b := idx.blocks[bi]
	mid := len(b.times) / 2
	b2 := &eventBlock{
		times:  append([]int64(nil), b.times[mid:]...),
		deltas: append([]int64(nil), b.deltas[mid:]...),
	}
	b.times = b.times[:mid:mid]
	b.deltas = b.deltas[:mid:mid]
	idx.blocks = append(idx.blocks, nil)
	copy(idx.blocks[bi+2:], idx.blocks[bi+1:])
	idx.blocks[bi+1] = b2
	idx.rebuild(b, idx.nextTime(bi))
	idx.rebuild(b2, idx.nextTime(bi+1))
}

// query 返回 [s, e)（裁剪到 [start, end)）内 V 的时长积分与 V 严格低于 threshold 的累计秒数。
func (idx *eventIndex) query(s, e int64, threshold int64) (integral int64, riskSeconds int64) {
	if s < idx.start {
		s = idx.start
	}
	if e > idx.end {
		e = idx.end
	}
	if s >= e {
		return 0, 0
	}
	bi := idx.findBlock(s)
	v := idx.base
	for j := 0; j < bi; j++ {
		v += idx.blocks[j].sumDelta
	}
	for ; bi < len(idx.blocks); bi++ {
		b := idx.blocks[bi]
		if b.times[0] >= e {
			break
		}
		if s <= b.times[0] && idx.nextTime(bi) <= e {
			// 整块被 [s, e) 覆盖：用聚合结构 O(log B) 计算。
			integral += v*b.sumDur + b.sumPrefDur
			x := threshold - v // V < threshold ⟺ pref < x（整数严格小于）
			k := sort.Search(len(b.sortedPref), func(i int) bool { return b.sortedPref[i] >= x })
			if k > 0 {
				riskSeconds += b.cumDur[k-1]
			}
			v += b.sumDelta
			continue
		}
		// 边界块：逐事件裁剪计算。
		for i := 0; i < len(b.times); i++ {
			v += b.deltas[i]
			segStart := b.times[i]
			segEnd := segStart + b.dur[i]
			if segEnd <= s {
				continue
			}
			if segStart >= e {
				break
			}
			lo, hi := segStart, segEnd
			if lo < s {
				lo = s
			}
			if hi > e {
				hi = e
			}
			integral += v * (hi - lo)
			if v < threshold {
				riskSeconds += hi - lo
			}
		}
	}
	return integral, riskSeconds
}
