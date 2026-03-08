package bot

import (
	"sync"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
)

// ShardRoundRobin coordinates turn-taking so one shard runs at a time.
type ShardRoundRobin struct {
	next uint32
	mu   sync.Mutex
}

// RunWithTurn runs work if this shard's turn, then advances.
func (r *ShardRoundRobin) RunWithTurn(session *discordgo.Session, getShardCount func() int, work func()) bool {
	shardCount := getShardCount()
	if shardCount <= 0 {
		shardCount = 1
	}
	if uint32(atomic.LoadUint32(&r.next)) != uint32(session.ShardID) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	nextVal := uint32((session.ShardID + 1) % shardCount)
	defer func() {
		if p := recover(); p != nil {
			atomic.StoreUint32(&r.next, nextVal)
			panic(p)
		}
	}()
	work()
	atomic.StoreUint32(&r.next, nextVal)
	return true
}
