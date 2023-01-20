package shardctrler

//
// Shardctrler clerk.
//

import (
	"6.824/labrpc"
	"github.com/sasha-s/go-deadlock"
)
import "time"
import "crypto/rand"
import "math/big"

type Clerk struct {
	servers []*labrpc.ClientEnd
	// Your data here.
	lastLeader int            // 上次的 leader，避免每次都要找
	clientId   int64          // nrand 唯一确定（总共没几个 client，足矣）
	requestId  int            // 从 0 递增
	mu         deadlock.Mutex // 单走一把锁
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// Your code here.
	// 初始 leader 设成 0
	ck.lastLeader = 0
	ck.clientId = nrand()
	ck.requestId = 0
	return ck
}

func (ck *Clerk) Query(num int) Config {
	ck.mu.Lock()
	defer ck.mu.Unlock()
	args := &QueryArgs{Num: num, ClientId: ck.clientId, RequestId: ck.requestId}
	for {
		// try lastLeader first
		var reply QueryReply
		ok := ck.servers[ck.lastLeader].Call("ShardCtrler.Query", args, &reply)
		if ok && reply.Err != ErrWrongLeader {
			ck.requestId += 1
			return reply.Config
		}
		// try each known server.
		for index, srv := range ck.servers {
			var reply QueryReply
			ok := srv.Call("ShardCtrler.Query", args, &reply)
			if ok && reply.Err != ErrWrongLeader {
				ck.requestId += 1
				ck.lastLeader = index
				return reply.Config
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ck *Clerk) Join(servers map[int][]string) {
	ck.mu.Lock()
	defer ck.mu.Unlock()
	args := &JoinArgs{Servers: servers, ClientId: ck.clientId, RequestId: ck.requestId}
	for {
		// try lastLeader first
		var reply JoinReply
		ok := ck.servers[ck.lastLeader].Call("ShardCtrler.Join", args, &reply)
		if ok && reply.Err != ErrWrongLeader {
			ck.requestId += 1
			return
		}
		// try each known server.
		for index, srv := range ck.servers {
			var reply JoinReply
			ok := srv.Call("ShardCtrler.Join", args, &reply)
			if ok && reply.Err != ErrWrongLeader {
				ck.requestId += 1
				ck.lastLeader = index
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ck *Clerk) Leave(gids []int) {
	ck.mu.Lock()
	defer ck.mu.Unlock()
	args := &LeaveArgs{GIDs: gids, ClientId: ck.clientId, RequestId: ck.requestId}
	for {
		// try lastLeader first
		var reply LeaveReply
		ok := ck.servers[ck.lastLeader].Call("ShardCtrler.Leave", args, &reply)
		if ok && reply.Err != ErrWrongLeader {
			ck.requestId += 1
			return
		}
		// try each known server.
		for index, srv := range ck.servers {
			var reply LeaveReply
			ok := srv.Call("ShardCtrler.Leave", args, &reply)
			if ok && reply.Err != ErrWrongLeader {
				ck.requestId += 1
				ck.lastLeader = index
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ck *Clerk) Move(shard int, gid int) {
	ck.mu.Lock()
	defer ck.mu.Unlock()
	args := &MoveArgs{Shard: shard, GID: gid, ClientId: ck.clientId, RequestId: ck.requestId}
	for {
		// try lastLeader first
		var reply MoveReply
		ok := ck.servers[ck.lastLeader].Call("ShardCtrler.Move", args, &reply)
		if ok && reply.Err != ErrWrongLeader {
			ck.requestId += 1
			return
		}
		// try each known server.
		for index, srv := range ck.servers {
			var reply MoveReply
			ok := srv.Call("ShardCtrler.Move", args, &reply)
			if ok && reply.Err != ErrWrongLeader {
				ck.requestId += 1
				ck.lastLeader = index
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
