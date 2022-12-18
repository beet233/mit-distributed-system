package kvraft

import (
	"6.824/labrpc"
	"github.com/sasha-s/go-deadlock"
)
import "crypto/rand"
import "math/big"

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
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
	// You'll have to add code here.
	// 初始 leader 设成 0
	ck.lastLeader = 0
	ck.clientId = nrand()
	ck.requestId = 0
	return ck
}

func (ck *Clerk) IncrementLastLeader() {
	ck.lastLeader += 1
	if ck.lastLeader > len(ck.servers)-1 {
		ck.lastLeader = 0
	}
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	ck.mu.Lock()
	defer ck.mu.Unlock()
	result := ""
	// call lastLeader 的 GET RPC
	for ; ; ck.IncrementLastLeader() {
		args := GetArgs{Key: key, ClientId: ck.clientId, RequestId: ck.requestId}
		reply := GetReply{}
		ok := ck.servers[ck.lastLeader].Call("KVServer.Get", &args, &reply)
		// TODO: 疑问，网络错误的话，我应该重试还是去试试下一个 server，目前这样是直接下一个
		if ok {
			if reply.Err == OK {
				result = reply.Value
				break
			} else if reply.Err == ErrNoKey {
				result = ""
				break
			}
			// 其他错误直接继续循环
		}
	}
	ck.requestId += 1
	return result
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	ck.mu.Lock()
	defer ck.mu.Unlock()
	// call lastLeader 的 GET RPC
	for ; ; ck.IncrementLastLeader() {
		args := PutAppendArgs{Key: key, Value: value, Op: op, ClientId: ck.clientId, RequestId: ck.requestId}
		reply := PutAppendReply{}
		ok := ck.servers[ck.lastLeader].Call("KVServer.PutAppend", &args, &reply)
		// TODO: 疑问，网络错误的话，我应该重试还是去试试下一个 server，目前这样是直接下一个
		if ok {
			if reply.Err == OK {
				break
			}
			// 其他错误直接继续循环
		}
	}
	ck.requestId += 1
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}
