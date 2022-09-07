package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, Term, isleader)
//   start agreement on a new log entry
// rf.GetState() (Term, isLeader)
//   ask a Raft for its current Term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"math/rand"
	//	"bytes"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labrpc"
)

//
// as each Raft peer becomes aware that successive log Entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	electionCurrentTime int
	electionDuration    int
	currentTerm         int
	votedFor            int

	// follower | candidate | leader
	status                  string
	voteSum                 int
	requestVoteReplyChannel chan RequestVoteReply
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	term = rf.currentTerm
	isleader = rf.status == "leader"
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term        int
	CandidateId int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	reply.VoteGranted = false
	if args.Term >= rf.currentTerm {
		if args.Term > rf.currentTerm {
			rf.currentTerm = args.Term
			rf.votedFor = -1
			rf.status = "follower"
			DPrintf("server %d become follower for term update\n", rf.me)
		}
		// TODO: guarantee if i am candidate or leader, do not vote
		if rf.votedFor == -1 || rf.votedFor == args.CandidateId {
			// TODO: if candidate's log is at least as up-to-date as mine, then grant vote
			reply.VoteGranted = true
			rf.votedFor = args.CandidateId
		}
	}
	reply.Term = rf.currentTerm
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendRequestVoteWithChannelReply(server int, args *RequestVoteArgs, reply *RequestVoteReply) {
	// 失败的就不传回 channel 了
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	if ok {
		rf.requestVoteReplyChannel <- *reply
	}
}

func (rf *Raft) requestVoteReplyConsumer() {
	for {
		reply := <-rf.requestVoteReplyChannel
		DPrintf("server %d get requestVoteReply\n", rf.me)
		if reply.Term > rf.currentTerm {
			rf.currentTerm = reply.Term
			rf.status = "follower"
			DPrintf("server %d become follower for term update\n", rf.me)
		}
		if reply.VoteGranted {
			DPrintf("server %d get vote from other server\n", rf.me)
			rf.voteSum++
		}
	}
}

type LogEntry struct {
}

type AppendEntriesArgs struct {
	Term     int
	LeaderId int
	Entries  []LogEntry
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

// AppendEntries RPC handler
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	DPrintf("server %d receive heartbeat from %d\n", rf.me, args.LeaderId)
	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.votedFor = -1
		rf.status = "follower"
		DPrintf("server %d become follower for term update\n", rf.me)
	}
	rf.electionCurrentTime = 0
	if rf.status == "candidate" {
		rf.status = "follower"
		DPrintf("server %d turn into follower from candidate\n", rf.me)
	}
	reply.Success = true
	if args.Term < rf.currentTerm {
		reply.Success = false
		return
	}
	if args.Entries != nil {
		// TODO: append log Entries
	}
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// Term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).

	return index, term, isLeader
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	// election timeout: 150ms - 300ms
	rf.electionDuration = 150 + rand.Intn(150)
	for rf.killed() == false && rf.status != "leader" {

		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		time.Sleep(time.Millisecond)
		rf.electionCurrentTime++
		if rf.electionCurrentTime >= rf.electionDuration {
			// kick off a new election
			DPrintf("server %d election timeout!\n", rf.me)
			DPrintf("server %d now become candidate\n", rf.me)
			rf.electionCurrentTime = 0
			go rf.election()
		}
		if rf.voteSum > len(rf.peers)/2 && rf.status == "candidate" {
			DPrintf("server %d get %d votes, len(rf.peers)/2 = %d, rf.status = \"%s\"\n", rf.me, rf.voteSum, len(rf.peers)/2, rf.status)
			DPrintf("server %d now become leader\n", rf.me)
			rf.status = "leader"
			// go a task to send heartbeats
			go rf.heartbeat()
		}
	}
}

// failed 掉的请求会一直卡在那里，应该改成如果收到回复，给voteSum++，在一个定时任务如ticker里监控voteSum，如果超过半数就转换，在成为新的candidate并term增加时清空voteSum
// 搞一个 channel 来接收返回，rpc 请求直接 go 出去
// The election process as candidate
func (rf *Raft) election() {
	rf.status = "candidate"
	rf.currentTerm++
	rf.voteSum = 0
	rf.votedFor = rf.me
	var args RequestVoteArgs
	args = RequestVoteArgs{rf.currentTerm, rf.me}
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			rf.voteSum++
			continue
		}
		var reply RequestVoteReply
		go rf.sendRequestVoteWithChannelReply(i, &args, &reply)
	}
}

// leader sending heartbeats task
func (rf *Raft) heartbeat() {
	DPrintf("server %d start giving heartbeats\n", rf.me)
	for rf.status == "leader" {
		var args AppendEntriesArgs
		args = AppendEntriesArgs{rf.currentTerm, rf.me, nil}
		// TODO: parallel
		for i := 0; i < len(rf.peers); i++ {
			if i == rf.me {
				continue
			}
			var reply AppendEntriesReply
			DPrintf("server %d send out heartbeat to %d\n", rf.me, i)
			ok := rf.sendAppendEntries(i, &args, &reply)
			if ok {
				if reply.Term > rf.currentTerm {
					rf.currentTerm = reply.Term
					rf.status = "follower"
					DPrintf("server %d become follower for term update\n", rf.me)
				}
			}
		}
		time.Sleep(time.Millisecond * 40)
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.status = "follower"
	rf.votedFor = -1
	// give it a 10 size buffer, try to prevent slow consuming impact
	rf.requestVoteReplyChannel = make(chan RequestVoteReply, 10)

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.ticker()
	DPrintf("server %d start to ticker as %s\n", me, rf.status)

	go rf.requestVoteReplyConsumer()

	return rf
}
