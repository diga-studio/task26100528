package risktrend

// employeeState 保存单名员工的固定属性与测评记录时间线。
type employeeState struct {
	weight  int64
	initial int
	records recordTreap
}

// recordNode 是员工测评记录 treap 的节点，按生效时间排序、按随机优先级堆序。
type recordNode struct {
	time        int64
	score       int
	prio        uint64
	left, right *recordNode
}

// recordTreap 维护单名员工按生效时间排序的测评记录，支持按时间查找与覆盖写入。
// 期望 O(log n)，不假设记录在员工间或时间上的分布。
type recordTreap struct {
	root *recordNode
	rng  uint64 // xorshift 状态，须非零
}

func (t *recordTreap) nextPrio() uint64 {
	x := t.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	t.rng = x
	return x
}

// lookup 查找时刻 tm：返回 tm 处的记录、tm 之前最近记录、tm 之后最近记录。
func (t *recordTreap) lookup(tm int64) (atScore int, hasAt bool, predScore int, hasPred bool, succTime int64, hasSucc bool) {
	n := t.root
	for n != nil {
		switch {
		case tm == n.time:
			atScore, hasAt = n.score, true
			for m := n.left; m != nil; m = m.right {
				predScore, hasPred = m.score, true
			}
			for m := n.right; m != nil; m = m.left {
				succTime, hasSucc = m.time, true
			}
			return
		case tm < n.time:
			succTime, hasSucc = n.time, true
			n = n.left
		default:
			predScore, hasPred = n.score, true
			n = n.right
		}
	}
	return
}

// set 覆盖已存在于 tm 的记录分数（调用方保证 tm 存在）。
func (t *recordTreap) set(tm int64, score int) {
	for n := t.root; n != nil; {
		switch {
		case tm == n.time:
			n.score = score
			return
		case tm < n.time:
			n = n.left
		default:
			n = n.right
		}
	}
}

// insert 插入一条新记录（调用方保证 tm 不存在）。
func (t *recordTreap) insert(tm int64, score int) {
	t.root = insertNode(t.root, &recordNode{time: tm, score: score, prio: t.nextPrio()})
}

func insertNode(n, nn *recordNode) *recordNode {
	if n == nil {
		return nn
	}
	if nn.time < n.time {
		n.left = insertNode(n.left, nn)
		if n.left.prio < n.prio {
			return rotateRight(n)
		}
	} else {
		n.right = insertNode(n.right, nn)
		if n.right.prio < n.prio {
			return rotateLeft(n)
		}
	}
	return n
}

func rotateRight(n *recordNode) *recordNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *recordNode) *recordNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}
