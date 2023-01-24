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
// LEAVING 状态下，kv 是不会被修改的。并且，在请求过程中，对方是上锁的，在我返回前，对方一定还处于 PULLING 状态。
// 这意味着，对方一定还没有发出 Leave RPC，我这边一定还没有删除，还处于 LEAVING 状态。
func (kv *ShardKV) Pull(args *PullArgs, reply *PullReply) {
	// is leader?
	_, isLeader := kv.rf.GetState()
	if isLeader {
		kv.mu.RLock()
		// is config version right ?
		if kv.currConfig.Num > args.ConfigVersion {
			// 如果对方还没拿到，我却已经升级到后续 config，那么错误
			log.Fatalln("Update to following config before others needing shards")
		} else if kv.currConfig.Num < args.ConfigVersion {
			reply.Err = ErrLowerConfigVersion
		} else {
			// is still exist ? 这里如果还存在，就意味着一定还在 LEAVING 了
			shard, exist := kv.shards[args.ShardId]
			if !exist {
				log.Fatalln("delete shard before others pull success")
			}
			reply.Storage = shard.storage
			reply.Err = OK
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
	if kv.currConfig.Num > args.ConfigVersion {
		log.Fatalln("Update to following config while others waiting")
	} else if kv.currConfig.Num < args.ConfigVersion {
		log.Fatalln("When get Leave RPC it should have same config version")
	} else {
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
				DPrintf("server %d timeout when handling Leave\n", kv.me)
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
}
