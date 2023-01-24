package shardkv

import (
	"6.824/labrpc"
	"6.824/shardctrler"
	"bytes"
	"fmt"
	"github.com/sasha-s/go-deadlock"
	"log"
	"time"
)
import "6.824/raft"
import "6.824/labgob"

// lab 说明给的建议轮询间隔
const FetchConfigInterval = 100

// 设定得和 raft time out 时间大致一样，有效等待 raft apply 完成
const RetryPullInterval = 200

const RaftTimeOut = 200

const Debug = true

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		rightHalf := fmt.Sprintf(format, a...)
		log.Printf("shardkv | %s", rightHalf)
	}
	return
}

const (
	GET    = 0
	PUT    = 1
	APPEND = 2
	// shard 相关 op
	UPDATE_CONFIG = 3 // WORKING -> others
	PULL_SHARD    = 4 // get data, PULLING -> WORKING
	LEAVE_SHARD   = 5 // remove data, LEAVING -> nil
	TRANSFER_DONE = 6 // confirm transfer done, WAITING -> WORKING
)

type OpType int

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Type OpType
	// for GET, PUT, APPEND
	Key       string
	Value     string
	ClientId  int64
	RequestId int
	// for UPDATE_CONFIG
	Config shardctrler.Config
	// for PULL_SHARD, LEAVE_SHARD, TRANSFER_DONE
	ShardId       int
	ConfigVersion int
	Storage       map[string]string
}

type ShardKV struct {
	// 这里可以改成一把读写锁，应该对性能不错
	mu           deadlock.RWMutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	ctrlers      []*labrpc.ClientEnd
	ctrlerClerk  *shardctrler.Clerk
	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	shards     map[int]*Shard
	prevConfig shardctrler.Config
	currConfig shardctrler.Config
	persister  *raft.Persister
	// raft log index -> wait channel
	waitChs                map[int]chan WaitChResponse
	latestAppliedRequest   map[int64]int // clientId -> requestId
	latestAppliedRaftIndex int
}

type Shard struct {
	storage map[string]string
	status  ShardStatus
}

type ShardStatus int

const (
	WORKING = 0 // 正常工作
	PULLING = 1 // 等待从之前的 group 获取
	LEAVING = 2 // 等待新的 group 确认获取
	WAITING = 3 // 等待之前的 group 删除
)

func (kv *ShardKV) serialize() []byte {
	DPrintf("server %d serialize to a snapshot\n", kv.me)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.latestAppliedRaftIndex)
	e.Encode(kv.latestAppliedRequest)
	e.Encode(kv.prevConfig)
	e.Encode(kv.currConfig)
	e.Encode(kv.shards)
	data := w.Bytes()
	return data
}

func (kv *ShardKV) deserialize(snapshot []byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if snapshot == nil || len(snapshot) < 1 {
		DPrintf("server %d has no snapshot to recover\n", kv.me)
		return
	}

	DPrintf("server %d read persister to recover\n", kv.me)

	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)

	var persistLatestAppliedRaftIndex int
	var persistLatestAppliedRequest map[int64]int
	var persistPrevConfig shardctrler.Config
	var persistCurrConfig shardctrler.Config
	var persistShards map[int]*Shard

	if d.Decode(&persistLatestAppliedRaftIndex) != nil || d.Decode(&persistLatestAppliedRequest) != nil ||
		d.Decode(&persistPrevConfig) != nil || d.Decode(&persistCurrConfig) != nil || d.Decode(&persistShards) != nil {
		DPrintf("server %d read persister error\n", kv.me)
	} else {
		kv.latestAppliedRaftIndex = persistLatestAppliedRaftIndex
		kv.latestAppliedRequest = persistLatestAppliedRequest
		kv.prevConfig = persistPrevConfig
		kv.currConfig = persistCurrConfig
		kv.shards = persistShards
	}
}

type WaitChResponse struct {
	err   Err
	value string // for Get
}

func (kv *ShardKV) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	raftIndex, _, isLeader := kv.rf.Start(Op{Type: GET, Key: args.Key, ClientId: args.ClientId, RequestId: args.RequestId})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		// 因为这个 Get 是个 RPC Handler，是要返回 reply 的，所以要等到 applyCh 传回关于这条指令的 msg 才行，建立以下通信机制
		kv.mu.Lock()
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
			DPrintf("server %d timeout when handling Get\n", kv.me)
			reply.Err = ErrWrongLeader
		case response := <-waitCh:
			reply.Value = response.value
			reply.Err = response.err
		}
		// 用完后把 waitCh 删掉
		kv.mu.Lock()
		delete(kv.waitChs, raftIndex)
		kv.mu.Unlock()
	}
}

func (kv *ShardKV) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	var opType OpType
	if args.Op == "Put" {
		opType = PUT
	} else {
		opType = APPEND
	}
	raftIndex, _, isLeader := kv.rf.Start(Op{Type: opType, Key: args.Key, Value: args.Value, ClientId: args.ClientId, RequestId: args.RequestId})
	if !isLeader {
		reply.Err = ErrWrongLeader
	} else {
		kv.mu.Lock()
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
			DPrintf("server %d timeout when handling PutAppend\n", kv.me)
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

//
// the tester calls Kill() when a ShardKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (kv *ShardKV) Kill() {
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *ShardKV) applyLoop() {
	for {
		applyMsg := <-kv.applyCh
		if applyMsg.CommandValid {
			if applyMsg.CommandIndex <= kv.latestAppliedRaftIndex {
				DPrintf("server %d get older command from applyCh\n", kv.me)
				continue
			}
			op := applyMsg.Command.(Op)
			waitChResponse := WaitChResponse{}
			// 需要注意处理重复的请求
			switch op.Type {
			case GET:
				kv.mu.Lock()
				// 对于读请求，要保证线性一致性，其实只要共识达成了就行，保证（在读请求发出前就完成的操作都被读到）
				// 在读请求返回前，如果因为发生了问题而间隔较长，返回的内容其实是灵活的，在满足线性一致性的前提下取决于实现
				// 我感觉读请求不需要做额外处理，如果是一次重试，那还是直接读现在的
				shard, shardExist := kv.shards[key2shard(op.Key)]
				// 如果 shard 不存在或者 shard 是 WORKING / WAITING 以外的状态，那么返回 ErrWrongGroup，让 client 重试
				if !shardExist || (shard.status != WORKING && shard.status != WAITING) {
					waitChResponse.err = ErrWrongGroup
				} else {
					_, exist := kv.latestAppliedRequest[op.ClientId]
					if !exist {
						kv.latestAppliedRequest[op.ClientId] = -1
					}
					if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
						kv.latestAppliedRequest[op.ClientId] = op.RequestId
					}
					value, keyExist := shard.storage[op.Key]
					if !keyExist {
						waitChResponse.err = ErrNoKey
					} else {
						waitChResponse.err = OK
						waitChResponse.value = value
					}
				}
				kv.mu.Unlock()
			case PUT:
				kv.mu.Lock()
				DPrintf("server %d put key: %s, value: %s\n", kv.me, op.Key, op.Value)
				shard, shardExist := kv.shards[key2shard(op.Key)]
				// 如果 shard 不存在或者 shard 是 WORKING / WAITING 以外的状态，那么返回 ErrWrongGroup，让 client 重试
				if !shardExist || (shard.status != WORKING && shard.status != WAITING) {
					waitChResponse.err = ErrWrongGroup
				} else {
					_, exist := kv.latestAppliedRequest[op.ClientId]
					if !exist {
						kv.latestAppliedRequest[op.ClientId] = -1
					}
					if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
						kv.latestAppliedRequest[op.ClientId] = op.RequestId
						shard.storage[op.Key] = op.Value
					}
					// 如果是重复的请求，就不做实际操作了，返回 OK 就行
					waitChResponse.err = OK
				}
				kv.mu.Unlock()
			case APPEND:
				kv.mu.Lock()
				DPrintf("server %d append key: %s, value: %s\n", kv.me, op.Key, op.Value)
				shard, shardExist := kv.shards[key2shard(op.Key)]
				// 如果 shard 不存在或者 shard 是 WORKING / WAITING 以外的状态，那么返回 ErrWrongGroup，让 client 重试
				if !shardExist || (shard.status != WORKING && shard.status != WAITING) {
					waitChResponse.err = ErrWrongGroup
				} else {
					_, exist := kv.latestAppliedRequest[op.ClientId]
					if !exist {
						kv.latestAppliedRequest[op.ClientId] = -1
					}
					if op.RequestId > kv.latestAppliedRequest[op.ClientId] {
						kv.latestAppliedRequest[op.ClientId] = op.RequestId
						value, keyExist := shard.storage[op.Key]
						if keyExist {
							shard.storage[op.Key] = value + op.Value
						} else {
							shard.storage[op.Key] = op.Value
						}
					}
					// 如果是重复的请求，就不做实际操作了，返回 OK 就行
					waitChResponse.err = OK
				}
				kv.mu.Unlock()
			case UPDATE_CONFIG:
				kv.mu.Lock()
				kv.prevConfig = kv.currConfig
				kv.currConfig = op.Config
				if op.Config.Num == 1 {
					// 第一份配置，直接创建空 map 开始 WORKING
					for shardId, gid := range op.Config.Shards {
						if gid == kv.gid {
							kv.shards[shardId] = &Shard{
								storage: map[string]string{},
								status:  WORKING,
							}
						}
					}
				} else {
					// 更新各个 shard 状态，pull 发起任务
					for shardId, gid := range op.Config.Shards {
						if gid == kv.gid {
							_, exist := kv.shards[shardId]
							// 如果是当前没有的，那么需要 PULL
							if !exist {
								kv.shards[shardId] = &Shard{
									storage: nil,
									status:  PULLING,
								}
								// TODO: 所有人发起 pull 任务（其实只有 leader 会实际执行），拿到数据后 raft 分享给 follower
								// 因为这里是上锁的，所以不能久留，pull 任务另 go 一个线程去做
							}
						}
					}
					for shardId, _ := range kv.shards {
						// 如果我现在的 shard 中，有 shard 在新 config 里不属于我，那么需要 LEAVE
						if op.Config.Shards[shardId] != kv.gid {
							kv.shards[shardId].status = LEAVING
						}
					}
				}
				kv.mu.Unlock()
			case PULL_SHARD:

			case LEAVE_SHARD:

			case TRANSFER_DONE:

			}
			kv.latestAppliedRaftIndex = applyMsg.CommandIndex
			if kv.maxraftstate != -1 && kv.persister.RaftStateSize() >= kv.maxraftstate {
				// 序列化 storage 和 latestAppliedRequest，调用 rf.Snapshot
				kv.rf.Snapshot(applyMsg.CommandIndex, kv.serialize())
			}
			// 如果返回了，却找不到这个 waitCh 了，那说明超时被放弃了，直接不传就行，下次遇到同样的就不执行
			kv.mu.RLock()
			waitCh, exist := kv.waitChs[applyMsg.CommandIndex]
			kv.mu.RUnlock()
			// 注意，其实只有 leader 有 waitCh，别的 follower 虽然也不断从 applyCh 里接收，但是是没有 waitCh 的
			// 同理，follower 们的 apply 到 storage 的过程实质上要在 applyLoop 进行
			if exist {
				waitCh <- waitChResponse
			} else {
				// DPrintf("server %d found no waitCh for raftIndex %d\n", kv.me, applyMsg.CommandIndex)
			}
		} else {
			// 如果快照比状态机还新，那直接更新到快照。
			// 如果快照比当前快照新，那其实 raft 那边在传过来前就已经切割完了 log 并存好了 raft state 和 service 的 snapshot，这边不用干。
			if applyMsg.SnapshotIndex > kv.latestAppliedRaftIndex {
				DPrintf("server %d get snapshot newer than state machine\n", kv.me)
				kv.deserialize(applyMsg.Snapshot)
				// kv.latestAppliedRaftIndex = applyMsg.SnapshotIndex
			}
		}
	}
}

func (kv *ShardKV) pullShard(shardId int) {
	for {
		kv.mu.Lock()
		if kv.shards[shardId].status != PULLING {
			kv.mu.Unlock()
			break
		}
		_, isLeader := kv.rf.GetState()
		if isLeader {
			// TODO: 发送 rpc 请求数据片，raft start 分享。注意这里也得循环 raft server 找 leader，并且需要从 prevConfig 找 group
		}
		kv.mu.Unlock()
		time.Sleep(time.Millisecond * RetryPullInterval)
	}
}

// 向老主人发送 Leave RPC，若 OK 则表示老主人已经 LEAVING -> nil，这边即可 Start 一个 TRANSFER_DONE，让 WAITING -> WORKING
func (kv *ShardKV) sendLeave(shardId int) {

}

// 每间隔 FetchConfigInterval 抓取一次 config，由 raft leader 来做
func (kv *ShardKV) fetchNextConfig() {
	for {
		_, isLeader := kv.rf.GetState()
		if isLeader {
			kv.mu.Lock()
			readForNext := true
			for _, shard := range kv.shards {
				if shard.status != WORKING {
					readForNext = false
				}
			}
			if readForNext {
				currVersion := kv.currConfig.Num
				latestConfig := kv.ctrlerClerk.Query(currVersion + 1)
				if latestConfig.Num > currVersion {
					if latestConfig.Num != currVersion+1 {
						log.Fatalln("Jump through some versions!")
					}
					// 更新 config 并标记各个 shard 状态，用 raft.Start 进 applyLoop 完成
					// 这边没有什么 waitCh 来通知是否完成，如果没更新成功自然会重试
					kv.rf.Start(Op{Type: UPDATE_CONFIG, Config: latestConfig})
					// kv.currConfig = latestConfig
					// if kv.currConfig.Num == 1 {
					// } else {
					// }
				}
			}
			kv.mu.Unlock()
		}
		time.Sleep(time.Millisecond * FetchConfigInterval)
	}
}

//
// servers[] contains the ports of the servers in this group.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
//
// the k/v server should snapshot when Raft's saved state exceeds
// maxraftstate bytes, in order to allow Raft to garbage-collect its
// log. if maxraftstate is -1, you don't need to snapshot.
//
// gid is this group's GID, for interacting with the shardctrler.
//
// pass ctrlers[] to shardctrler.MakeClerk() so you can send
// RPCs to the shardctrler.
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs. You'll need this to send RPCs to other groups.
//
// look at client.go for examples of how to use ctrlers[]
// and make_end() to send RPCs to the group owning a specific shard.
//
// StartServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int, gid int, ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *ShardKV {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(ShardKV)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.make_end = make_end
	kv.gid = gid
	kv.ctrlers = ctrlers

	// Your initialization code here.
	kv.ctrlerClerk = shardctrler.MakeClerk(ctrlers)
	kv.shards = make(map[int]*Shard)
	// 初始化为和 shardctrler 一模一样的 invalid config 0
	kv.currConfig = shardctrler.Config{Groups: map[int][]string{}}
	kv.prevConfig = kv.currConfig
	kv.persister = persister
	kv.waitChs = make(map[int]chan WaitChResponse)
	kv.latestAppliedRequest = make(map[int64]int)
	kv.latestAppliedRaftIndex = 0

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	snapshot := persister.ReadSnapshot()
	kv.deserialize(snapshot)

	go kv.fetchNextConfig()
	go kv.applyLoop()
	return kv
}
