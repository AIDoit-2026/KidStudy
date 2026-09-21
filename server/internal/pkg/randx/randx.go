// Package randx 提供「同一 seed 必得同一序列」的伪随机数发生器。
//
// 为什么不用 math/rand：math/rand 的全局源在 Go 1.20 之后会自动随机播种，
// 且不同版本的实现细节不保证稳定；而数学题必须满足「同一 seed 两次生成完全一致」
// （《开发设计文档》§4.2：屏幕练习与打印中心同源，seed 可复现）。
//
// 实现用 splitmix64：状态只有 8 字节、无外部依赖、序列质量足够做题目参数抽样。
package randx

import (
	"encoding/binary"
	"math"
)

// Rand 是一个独立的确定性随机源，不可并发共享（题目生成都是单线程顺序调用）。
type Rand struct {
	state uint64
}

// New 用给定 seed 构造随机源。
func New(seed uint64) *Rand { return &Rand{state: seed} }

// NewFromBytes 从任意字节串派生 seed，便于把 uuid / 字符串当种子。
func NewFromBytes(b []byte) *Rand { return New(FNV64a(b)) }

// Uint64 返回下一个 64 位值（splitmix64）。
func (r *Rand) Uint64() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn 返回 [0, n) 内的整数。n <= 0 时返回 0。
func (r *Rand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	// 取高位避免低位周期短带来的偏斜
	return int(r.Uint64()>>32) % n
}

// Range 返回 [lo, hi] 内的整数（闭区间）。lo > hi 时返回 lo。
func (r *Rand) Range(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + r.Intn(hi-lo+1)
}

// Float64 返回 [0, 1) 内的浮点数。
func (r *Rand) Float64() float64 {
	return float64(r.Uint64()>>11) / (1 << 53)
}

// Pick 从切片中等概率取一个元素。空切片返回零值。
func Pick[T any](r *Rand, xs []T) T {
	var zero T
	if len(xs) == 0 {
		return zero
	}
	return xs[r.Intn(len(xs))]
}

// Shuffle 原地洗牌（Fisher-Yates，用本包的确定性源）。
func Shuffle[T any](r *Rand, xs []T) {
	for i := len(xs) - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// Sample 从 xs 中不重复抽取 n 个（n >= len(xs) 时返回全部的一个洗牌副本）。
func Sample[T any](r *Rand, xs []T, n int) []T {
	out := make([]T, len(xs))
	copy(out, xs)
	Shuffle(r, out)
	if n < 0 {
		n = 0
	}
	if n > len(out) {
		n = len(out)
	}
	return out[:n]
}

// Derive 从父 seed 派生第 i 个子 seed，保证「同一 (seed, i) 恒等」。
//
// 题目生成器按「第 i 题 = Derive(seed, i)」的方式取值，这样即使中途插入或删除题目，
// 其余题目的内容也不会漂移 —— 打印预览与正式下发能逐题对上。
func Derive(seed uint64, i int) uint64 {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[:8], seed)
	binary.BigEndian.PutUint64(buf[8:], uint64(i))
	h := FNV64a(buf[:])
	// 再混一轮 splitmix，避免 FNV 低位相关性在小范围内暴露
	z := h
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// FNV64a 是稳定的 64 位 FNV-1a 哈希，用于把任意字节串折叠成 seed。
func FNV64a(b []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return h
}

// SeedFromInts 把一组整数折叠成一个 seed（用于 child/日期/题型等组合）。
func SeedFromInts(parts ...uint64) uint64 {
	h := uint64(14695981039346656037)
	for _, p := range parts {
		h ^= p
		h *= 1099511628211
	}
	if h == 0 {
		h = math.MaxUint64 / 2
	}
	return h
}
