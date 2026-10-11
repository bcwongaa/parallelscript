// Binary trees, the twin of main.pts.
package main

import "fmt"

type treeNode struct {
	left, right *treeNode
}

func bottomUpTree(depth int) *treeNode {
	if depth > 0 {
		return &treeNode{bottomUpTree(depth - 1), bottomUpTree(depth - 1)}
	}
	return &treeNode{nil, nil}
}

func itemCheck(node *treeNode) int {
	if node.left == nil {
		return 1
	}
	return 1 + itemCheck(node.left) + itemCheck(node.right)
}

func main() {
	const minDepth, maxDepth = 4, 18
	stretchDepth := maxDepth + 1
	fmt.Printf("stretch tree of depth %d\t check: %d\n", stretchDepth, itemCheck(bottomUpTree(stretchDepth)))
	longLivedTree := bottomUpTree(maxDepth)
	for depth := minDepth; depth <= maxDepth; depth += 2 {
		iterations := 1 << (maxDepth - depth + minDepth)
		check := 0
		for i := 0; i < iterations; i++ {
			check += itemCheck(bottomUpTree(depth))
		}
		fmt.Printf("%d\t trees of depth %d\t check: %d\n", iterations, depth, check)
	}
	fmt.Printf("long lived tree of depth %d\t check: %d\n", maxDepth, itemCheck(longLivedTree))
}
