// Package idgen 提供雪花算法(Snowflake)分布式 ID 生成器.
// 用于 M2 二维码核销等业务流水号生成, 保证集群内全局唯一且趋势递增.
package idgen

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

// 时间起点(毫秒): 2023-11-14T00:00:00Z. 可按项目实际上线时间调整.
const epochMillis = int64(1_700_000_000_000)

const (
	workerBits  = 10 // 工作节点 10 位(整合 datacenter+worker), 支持 0~1023
	sequenceBits = 12 // 序列号 12 位, 每毫秒单节点 4096 个

	maxWorkerID  = (1 << workerBits) - 1
	sequenceMask = (1 << sequenceBits) - 1
)

// Generator 雪花 ID 生成器(协程安全).
type Generator struct {
	mu       sync.Mutex
	workerID int64
	lastTS   int64
	seq      int64
}

// NewGenerator 创建生成器. workerID 取值 [0, 1023], 需保证集群内各节点唯一.
func NewGenerator(workerID int64) (*Generator, error) {
	if workerID < 0 || workerID > maxWorkerID {
		return nil, errors.New("idgen: workerID out of range [0,1023]")
	}
	return &Generator{workerID: workerID}, nil
}

// NextID 生成下一个 int64 雪花 ID.
func (g *Generator) NextID() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ts := time.Now().UnixMilli()
	if ts < g.lastTS {
		return 0, errors.New("idgen: clock moved backwards")
	}

	if ts == g.lastTS {
		g.seq = (g.seq + 1) & sequenceMask
		if g.seq == 0 {
			// 当前毫秒序列用尽, 自旋等待下一毫秒.
			for ts <= g.lastTS {
				ts = time.Now().UnixMilli()
			}
		}
	} else {
		g.seq = 0
	}
	g.lastTS = ts

	id := ((ts - epochMillis) << (workerBits + sequenceBits)) |
		(g.workerID << sequenceBits) |
		g.seq
	return id, nil
}

// NextIDStr 生成字符串形式的雪花 ID(便于二维码/URL 透传).
func (g *Generator) NextIDStr() (string, error) {
	id, err := g.NextID()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}
