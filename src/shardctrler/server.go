package shardctrler

import (
	"6.824/raft"
	"fmt"
	"log"
	"sort"
	"time"
)
import "6.824/labrpc"
import "sync"
import "6.824/labgob"

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		rightHalf := fmt.Sprintf(format, a...)
		log.Printf("shardctrler | %s", rightHalf)
	}
	return
}

type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	// Your data here.

	configs []Config // indexed by config num, configs[0] is very first config with no real meaning
	// raft log index -> wait channel
	waitChs                map[int]chan WaitChResponse
	latestAppliedRequest   map[int64]int // clientId -> requestId
	latestAppliedRaftIndex int
}

const TimeOut = 200

type OpType int

const (
	JOIN  = 0
	LEAVE = 1
	MOVE  = 2
	QUERY = 3
)

type Op struct {
	// Your data here.
	Type      OpType
	ClientId  int64
	RequestId int
	JoinArgs  JoinArgs
	LeaveArgs LeaveArgs
	MoveArgs  MoveArgs
	QueryArgs QueryArgs
}

type WaitChResponse struct {
	err Err
	// 某些 reply 需要的值加在这里，由 applyLoop 应用到状态机之后返回到 RPC Handler
	config Config
}

func (sc *ShardCtrler) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
	raftIndex, _, isLeader := sc.rf.Start(Op{Type: JOIN, ClientId: args.ClientId, RequestId: args.RequestId, JoinArgs: *args})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		sc.mu.Lock()
		waitCh, exist := sc.waitChs[raftIndex]
		if exist {
			log.Fatalf("shardctrler server %d try to get a existing waitCh\n", sc.me)
		}
		sc.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = sc.waitChs[raftIndex]
		sc.mu.Unlock()
		select {
		case <-time.After(time.Millisecond * TimeOut):
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Err = response.err
		}
		// 用完后把 waitCh 删掉
		sc.mu.Lock()
		delete(sc.waitChs, raftIndex)
		sc.mu.Unlock()
	}
}

func (sc *ShardCtrler) Leave(args *LeaveArgs, reply *LeaveReply) {
	// Your code here.
	raftIndex, _, isLeader := sc.rf.Start(Op{Type: LEAVE, ClientId: args.ClientId, RequestId: args.RequestId, LeaveArgs: *args})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		sc.mu.Lock()
		waitCh, exist := sc.waitChs[raftIndex]
		if exist {
			log.Fatalf("shardctrler server %d try to get a existing waitCh\n", sc.me)
		}
		sc.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = sc.waitChs[raftIndex]
		sc.mu.Unlock()
		select {
		case <-time.After(time.Millisecond * TimeOut):
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Err = response.err
		}
		// 用完后把 waitCh 删掉
		sc.mu.Lock()
		delete(sc.waitChs, raftIndex)
		sc.mu.Unlock()
	}
}

func (sc *ShardCtrler) Move(args *MoveArgs, reply *MoveReply) {
	// Your code here.
	raftIndex, _, isLeader := sc.rf.Start(Op{Type: MOVE, ClientId: args.ClientId, RequestId: args.RequestId, MoveArgs: *args})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		sc.mu.Lock()
		waitCh, exist := sc.waitChs[raftIndex]
		if exist {
			log.Fatalf("shardctrler server %d try to get a existing waitCh\n", sc.me)
		}
		sc.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = sc.waitChs[raftIndex]
		sc.mu.Unlock()
		select {
		case <-time.After(time.Millisecond * TimeOut):
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Err = response.err
		}
		// 用完后把 waitCh 删掉
		sc.mu.Lock()
		delete(sc.waitChs, raftIndex)
		sc.mu.Unlock()
	}
}

func (sc *ShardCtrler) Query(args *QueryArgs, reply *QueryReply) {
	// Your code here.
	raftIndex, _, isLeader := sc.rf.Start(Op{Type: QUERY, ClientId: args.ClientId, RequestId: args.RequestId, QueryArgs: *args})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		sc.mu.Lock()
		waitCh, exist := sc.waitChs[raftIndex]
		if exist {
			log.Fatalf("shardctrler server %d try to get a existing waitCh\n", sc.me)
		}
		sc.waitChs[raftIndex] = make(chan WaitChResponse, 1)
		waitCh = sc.waitChs[raftIndex]
		sc.mu.Unlock()
		select {
		case <-time.After(time.Millisecond * TimeOut):
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Err = response.err
			reply.Config = response.config
		}
		// 用完后把 waitCh 删掉
		sc.mu.Lock()
		delete(sc.waitChs, raftIndex)
		sc.mu.Unlock()
	}
}

//
// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (sc *ShardCtrler) Kill() {
	sc.rf.Kill()
	// Your code here, if desired.
}

// needed by shardkv tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

func createNewConfigDeepCopy(config Config) Config {
	newConfig := Config{}
	newConfig.Num = config.Num + 1
	for i := 0; i < NShards; i++ {
		newConfig.Shards[i] = config.Shards[i]
	}
	newConfig.Groups = make(map[int][]string)
	// 注意，go 里数组间赋值是直接复制所有值，而不是赋予指针，所以以下代码可以实现深拷贝
	for key, value := range config.Groups {
		newConfig.Groups[key] = value
	}
	return newConfig
}

// 因为 map 的遍历顺序是有随机性的（各个 server 完全可能不同），会导致不同 server 的 rebalance 分片结果不同
// 因此，我们需要把 gid 搞一个有序的列表
func rebalanceShard(groups map[int][]string, shards [NShards]int, isJoin bool, isFirst bool) [NShards]int {
	// golang 中这是深拷贝的
	rebalancedShards := shards
	groupToShards := make(map[int][]int)
	sortedGIDs := make([]int, 0)
	// TODO: map 的 exist 具体是怎么回事，golang 的 空 / 零值 都存疑
	// 把空数组 make 出来，之后这个 gid 就 exist 于 groupToShards 了
	for gid, _ := range groups {
		groupToShards[gid] = make([]int, 0)
		sortedGIDs = append(sortedGIDs, gid)
	}
	sort.Ints(sortedGIDs)
	for shardIndex, gid := range shards {
		_, exist := groupToShards[gid]
		if exist {
			groupToShards[gid] = append(groupToShards[gid], shardIndex)
		}
	}
	if isJoin && !isFirst {
		// 需要填充一些空 group 的情况
		// 一片片地把实时最多的 group 的片搬到实时最少的 group，但这个策略在 JOIN 第一个 group 时不管用，单独处理
		for {
			maxGid := -1
			maxShardCount := 0
			minGid := -1
			minShardCount := NShards + 1
			for _, gid := range sortedGIDs {
				shards := groupToShards[gid]
				if len(shards) >= maxShardCount {
					maxShardCount = len(shards)
					maxGid = gid
				}
				if len(shards) <= minShardCount {
					minShardCount = len(shards)
					minGid = gid
				}
			}
			if maxShardCount-minShardCount <= 1 {
				break
			} else {
				// 把最多的 group 分一个 shard 给最少的
				groupToShards[minGid] = append(groupToShards[minGid], groupToShards[maxGid][0])
				groupToShards[maxGid] = groupToShards[maxGid][1:]
			}
		}
	} else if len(groups) > 0 {
		// 需要分配一些多余的无主的片的情况（ LEAVE 或 第一次 JOIN ）
		// 把多余出来的片一片片地加入实时最少的 group
		// 孤儿片
		orphanShards := make([]int, 0)
		for shardIndex, gid := range shards {
			_, exist := groupToShards[gid]
			if !exist {
				orphanShards = append(orphanShards, shardIndex)
			}
		}
		for len(orphanShards) > 0 {
			minGid := -1
			minShardCount := NShards + 1
			for _, gid := range sortedGIDs {
				shards := groupToShards[gid]
				if len(shards) <= minShardCount {
					minShardCount = len(shards)
					minGid = gid
				}
			}
			groupToShards[minGid] = append(groupToShards[minGid], orphanShards[0])
			orphanShards = orphanShards[1:]
		}
	} else {
		// 最后一次 LEAVE (清空 group) 时需要把 shards 均置给 invalid group 0
		for i := 0; i < NShards; i++ {
			rebalancedShards[i] = 0
		}
	}
	for gid, shards := range groupToShards {
		for _, shard := range shards {
			rebalancedShards[shard] = gid
		}
	}
	return rebalancedShards
}

func (sc *ShardCtrler) applyLoop() {
	for {
		applyMsg := <-sc.applyCh
		if applyMsg.CommandValid {
			if applyMsg.CommandIndex <= sc.latestAppliedRaftIndex {
				DPrintf("server %d get older command from applyCh\n", sc.me)
				continue
			}
			op := applyMsg.Command.(Op) // 强制类型转换成 Op
			waitChResponse := WaitChResponse{}
			// 判断是否是重复请求，根据 op 中的 requestId 和 clientId
			duplicated := false
			_, exist := sc.latestAppliedRequest[op.ClientId]
			if !exist {
				sc.latestAppliedRequest[op.ClientId] = -1
			}
			if op.RequestId > sc.latestAppliedRequest[op.ClientId] {
				sc.latestAppliedRequest[op.ClientId] = op.RequestId
			} else {
				duplicated = true
			}
			// 实际执行策略 ↓
			switch op.Type {
			case JOIN:
				if duplicated {
					waitChResponse.err = OK
				} else {
					// 是否是第一次 JOIN
					isFirst := false
					newConfig := createNewConfigDeepCopy(sc.configs[len(sc.configs)-1])
					if len(newConfig.Groups) == 0 {
						isFirst = true
					}
					for gid, servers := range op.JoinArgs.Servers {
						DPrintf("add group %v\n", gid)
						newConfig.Groups[gid] = servers
					}
					// rebalance shards
					newConfig.Shards = rebalanceShard(newConfig.Groups, newConfig.Shards, true, isFirst)
					sc.configs = append(sc.configs, newConfig)
					waitChResponse.err = OK
				}
			case LEAVE:
				if duplicated {
					waitChResponse.err = OK
				} else {
					newConfig := createNewConfigDeepCopy(sc.configs[len(sc.configs)-1])
					for _, gid := range op.LeaveArgs.GIDs {
						DPrintf("remove group %v\n", gid)
						delete(newConfig.Groups, gid)
					}
					// rebalance shards
					newConfig.Shards = rebalanceShard(newConfig.Groups, newConfig.Shards, false, false)
					sc.configs = append(sc.configs, newConfig)
					waitChResponse.err = OK
				}
			case MOVE:
				if duplicated {
					waitChResponse.err = OK
				} else {
					newConfig := createNewConfigDeepCopy(sc.configs[len(sc.configs)-1])
					newConfig.Shards[op.MoveArgs.Shard] = op.MoveArgs.GID
					sc.configs = append(sc.configs, newConfig)
					waitChResponse.err = OK
				}
			case QUERY:
				// QUERY 不用在意 duplicated，直接正常返回就行
				if op.QueryArgs.Num < 0 || op.QueryArgs.Num >= len(sc.configs)-1 {
					waitChResponse.config = sc.configs[len(sc.configs)-1]
				} else {
					waitChResponse.config = sc.configs[op.QueryArgs.Num]
				}
				waitChResponse.err = OK
			}
			sc.mu.Lock()
			sc.latestAppliedRaftIndex = applyMsg.CommandIndex
			waitCh, exist := sc.waitChs[applyMsg.CommandIndex]
			sc.mu.Unlock()
			if exist {
				waitCh <- waitChResponse
			}
		} else {
			// 不使用也不考虑 snapshot，因为 config change 量比较小
		}
	}
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	sc := new(ShardCtrler)
	sc.me = me

	sc.configs = make([]Config, 1)
	sc.configs[0].Groups = map[int][]string{}

	labgob.Register(Op{})
	sc.applyCh = make(chan raft.ApplyMsg)
	sc.rf = raft.Make(servers, me, persister, sc.applyCh)

	// Your code here.
	sc.waitChs = make(map[int]chan WaitChResponse)
	sc.latestAppliedRequest = make(map[int64]int)
	sc.latestAppliedRaftIndex = 0
	// 因为这里也是个基于 raft 的，所以也是需要一个处理 applyCh 的线程
	go sc.applyLoop()
	return sc
}
