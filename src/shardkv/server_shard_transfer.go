package shardkv

import (
	"log"
	"time"
)

// Pull -> 请求目标 group 将 shard 传给自己
// Leave -> 请求目标 group 删除 shard，表示确认已经转移到自己

type PullArgs struct {
	ConfigVersion int
	ShardId       int
}

type PullReply struct {
	Storage map[string]string
	Err     Err
}

type LeaveArgs struct {
	ConfigVersion int
	ShardId       int
}

type LeaveReply struct {
	Err Err
}

// 这里读整个 storage 就没必要走 raft 了，只需要规定：仅当 status 为 LEAVING 时，接受请求并给出数据片。
// LEAVING 状态下，kv 是不会被修改的。
// 按理说，如果这边已经 LEAVING -> nil 了，说明对方一定已经出现了 PULLING -> WAITING 的 leader，也就是已经完成了数据获取。
// 在 nil 的情况下如果还收到 Pull RPC，说明对方应该出现了脑裂之类的问题，有裂开的老 leader 之类的在发请求。这时返回个 Err 让它重试就行。
// 等它恢复了之后，自然会摆脱重试。
func (kv *ShardKV) Pull(args *PullArgs, reply *PullReply) {
	// is leader?
	_, isLeader := kv.rf.GetState()
	if isLeader {
		kv.mu.RLock()
		// is config version right ?
		if kv.currConfig.Num > args.ConfigVersion {
			// 如果对方还没拿到，我却已经升级到后续 config，那么应该是对方出现了分区等错误，让它重试
			// log.Fatalln("Update to following config before others needing shards")
			reply.Err = ErrConfigVersion
		} else if kv.currConfig.Num < args.ConfigVersion {
			reply.Err = ErrConfigVersion
		} else {
			// is still exist ? 这里如果还存在，就意味着一定还在 LEAVING 了
			shard, exist := kv.shards[args.ShardId]
			if !exist {
				// log.Fatalln("delete shard before others pull success")
				reply.Err = ErrConfigVersion
			} else {
				// copy map，因为 RPC 传输过程中会不上锁地读原来的 map
				reply.Storage = map[string]string{}
				for key, value := range shard.Storage {
					reply.Storage[key] = value
				}
				reply.Err = OK
			}
		}
		kv.mu.RUnlock()
	} else {
		reply.Err = ErrWrongLeader
	}
}

// Leave 这边收到消息后就可以删除 shard 并返回，让新主人 WAITING -> WORKING 了，但是删除 shard 是需要进 raft 来通知 follower 的
// raft 可能失败（我不再是 leader），也可能缓慢，我们应该保证真正删除后再返回 OK。这又涉及到 applyLoop 和这里的通信问题，再次请出 waitCh
func (kv *ShardKV) Leave(args *LeaveArgs, reply *LeaveReply) {
	kv.mu.Lock()
	// 用 raft 保证所有 replica 都达成删除共识
	raftIndex, _, isLeader := kv.rf.Start(Op{Type: LEAVE_SHARD, ShardId: args.ShardId, ConfigVersion: kv.currConfig.Num})
	if !isLeader {
		reply.Err = ErrWrongLeader
		kv.mu.Unlock()
	} else {
		waitCh, exist := kv.waitChs[raftIndex]
		if exist {
			log.Fatalf("shardkv | server %d try to get a existing waitCh\n", kv.me)
		}
		kv.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = kv.waitChs[raftIndex]
		kv.mu.Unlock()
		// 选择在 server 端来判断超时，如果超时了还没返回，共识失败，认为 ErrWrongLeader
		select {
		case <-time.After(time.Millisecond * RaftTimeOut):
			kv.DPrintf("server %d timeout when handling Leave\n", kv.me)
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Err = response.err
		}
		// 用完后把 waitCh 删掉
		kv.mu.Lock()
		delete(kv.waitChs, raftIndex)
		kv.mu.Unlock()
	}
}
