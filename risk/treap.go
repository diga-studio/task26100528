package risk

import "math/rand"

// treapNode 是 eventTreap 的节点，key 为生效时间。
type treapNode struct {
	time        int64
	score       int
	prio        uint32
	left, right *treapNode
}

// eventTreap 是单名员工测评记录的随机平衡树，按生效时间排序。
// 支持插入/覆盖、前驱查询与后继查询，均为 O(log n)。
type eventTreap struct {
	root *treapNode
	rnd  *rand.Rand
}

func newEventTreap(rnd *rand.Rand) *eventTreap {
	return &eventTreap{rnd: rnd}
}

// get 返回恰好生效于 time 的测评分数。
func (t *eventTreap) get(time int64) (int, bool) {
	n := t.root
	for n != nil {
		switch {
		case time < n.time:
			n = n.left
		case time > n.time:
			n = n.right
		default:
			return n.score, true
		}
	}
	return 0, false
}

// floor 返回生效时间不晚于 time 的最新测评分数。
func (t *eventTreap) floor(time int64) (int, bool) {
	n := t.root
	best := -1
	found := false
	for n != nil {
		if n.time <= time {
			best = n.score
			found = true
			n = n.right
		} else {
			n = n.left
		}
	}
	return best, found
}

// successor 返回严格晚于 time 的最早生效时间。
func (t *eventTreap) successor(time int64) (int64, bool) {
	n := t.root
	var best int64
	found := false
	for n != nil {
		if n.time > time {
			best = n.time
			found = true
			n = n.left
		} else {
			n = n.right
		}
	}
	return best, found
}

// forEach 按生效时间升序遍历全部测评。
func (t *eventTreap) forEach(fn func(time int64, score int)) {
	var walk func(n *treapNode)
	walk = func(n *treapNode) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.time, n.score)
		walk(n.right)
	}
	walk(t.root)
}

// set 写入一条测评；同一时间已存在则覆盖原分数。
func (t *eventTreap) set(time int64, score int) {
	if n := t.root; n != nil {
		if cur, ok := t.findNode(time); ok {
			cur.score = score
			return
		}
	}
	node := &treapNode{time: time, score: score, prio: t.rnd.Uint32()}
	t.root = insertNode(t.root, node)
}

func (t *eventTreap) findNode(time int64) (*treapNode, bool) {
	n := t.root
	for n != nil {
		switch {
		case time < n.time:
			n = n.left
		case time > n.time:
			n = n.right
		default:
			return n, true
		}
	}
	return nil, false
}

func insertNode(root, node *treapNode) *treapNode {
	if root == nil {
		return node
	}
	if node.time < root.time {
		root.left = insertNode(root.left, node)
		if root.left.prio < root.prio {
			return rotateRight(root)
		}
	} else {
		root.right = insertNode(root.right, node)
		if root.right.prio < root.prio {
			return rotateLeft(root)
		}
	}
	return root
}

func rotateRight(n *treapNode) *treapNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *treapNode) *treapNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}
