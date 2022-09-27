package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	//	"bytes"

	"sync/atomic"

	//	"6.824/labgob"
	"6.824/labrpc"
)

//
// as each Raft peer becomes aware that successive log entries are
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
	// mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	raftState *RaftState

	// some log flags with wrapped log func
	electionDebug       bool
	logReplicationDebug bool
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	rf.raftState.rLock()
	term = rf.raftState.currentTerm
	isleader = rf.raftState.isState(leaderState)
	rf.raftState.rUnlock()
	return term, isleader
}

func (rf *Raft) logWithRaftStatus(format string, vars ...interface{}) {
	var stateString string
	state := rf.raftState.state
	switch state {
	case candidateState:
		stateString = "candidate"
		break
	case leaderState:
		stateString = "leader"
		break
	case followerState:
		stateString = "follower"
		break
	default:
		log.Fatal("undefined raft state.")
		break
	}
	rightHalf := fmt.Sprintf(format, vars...)
	log.Printf("server %d %s in term %d votedFor %d | %s", rf.me, stateString, rf.raftState.currentTerm, rightHalf)
}

func (rf *Raft) electionLog(format string, vars ...interface{}) {
	if rf.electionDebug {
		rf.logWithRaftStatus(format, vars...)
	}
}

func (rf *Raft) logReplicationLog(format string, vars ...interface{}) {
	if rf.logReplicationDebug {
		rf.logWithRaftStatus(format, vars...)
	}
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
	rf.raftState.wLock()
	if args.Term >= rf.raftState.currentTerm {
		if args.Term > rf.raftState.currentTerm {
			rf.electionLog("update term and become follower if needed\n")
			rf.raftState.currentTerm = args.Term
			rf.raftState.votedFor = -1
			rf.raftState.state = followerState
		}
		if rf.raftState.votedFor == -1 || rf.raftState.votedFor == args.CandidateId {
			// TODO: if candidate's log is at least as up-to-date as mine, then grant vote
			rf.electionLog("vote for %d\n", args.CandidateId)
			reply.VoteGranted = true
			rf.raftState.votedFor = args.CandidateId
		}
	}
	reply.Term = rf.raftState.currentTerm
	rf.raftState.wUnlock()
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
// func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
// 	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
// 	return ok
// }

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
// term. the third return value is true if this server believes it is
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
// func (rf *Raft) ticker() {
// 	for rf.killed() == false {

// 		// Your code here to check if a leader election should
// 		// be started and to randomize sleeping time using
// 		// time.Sleep().

// 	}
// }

// 以 10 ms 为一个循环单位，循环单位太短可能会因为代码运行时间而影响实际时间
const loopTimeUnit = 10

func (rf *Raft) leaderMain() {

}

// warpped reply for check and retry
type WrappedRequestVoteReply struct {
	ok    bool
	from  int
	args  RequestVoteArgs
	reply RequestVoteReply
}

func (rf *Raft) sendRequestVoteToAll() {
	grantedVoteSum := 1
	rf.raftState.rLock()
	thisTerm := rf.raftState.currentTerm
	rf.raftState.rUnlock()
	replyChannel := make(chan WrappedRequestVoteReply, 10)
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		rf.raftState.rLock()
		args := RequestVoteArgs{rf.raftState.currentTerm, rf.me}
		rf.raftState.rUnlock()
		var reply RequestVoteReply
		go rf.sendRequestVoteWithChannelReply(i, &args, &reply, replyChannel)
	}
	// process reply
	for {
		rf.raftState.rLock()
		if rf.raftState.currentTerm > thisTerm || rf.raftState.state != candidateState {
			rf.raftState.rUnlock()
			break
		}
		rf.raftState.rUnlock()
		reply := <-replyChannel
		if reply.ok {
			rf.electionLog("get reply from server %d\n", reply.from)
			rf.raftState.wLock()
			if reply.reply.Term > rf.raftState.currentTerm {
				rf.raftState.currentTerm = reply.reply.Term
				rf.raftState.state = followerState
			}
			rf.raftState.wUnlock()
			if reply.reply.VoteGranted {
				grantedVoteSum += 1
				if grantedVoteSum > len(rf.peers)/2 {
					rf.electionLog("become leader\n")
					rf.raftState.wLock()
					rf.raftState.state = leaderState
					rf.raftState.wUnlock()
				}
			}
		} else {
			// retry
			var retryReply RequestVoteReply
			go rf.sendRequestVoteWithChannelReply(reply.from, &reply.args, &retryReply, replyChannel)
		}
	}
}

func (rf *Raft) sendRequestVoteWithChannelReply(to int, args *RequestVoteArgs, reply *RequestVoteReply, replyChannel chan WrappedRequestVoteReply) {
	// 失败的也要传回 channel ，提供 retry 的契机
	ok := rf.peers[to].Call("Raft.RequestVote", args, reply)
	wrappedReply := WrappedRequestVoteReply{ok, to, *args, *reply}
	replyChannel <- wrappedReply
}

func (rf *Raft) doElection() {
	rf.raftState.wLock()
	rf.raftState.currentTerm += 1
	rf.raftState.votedFor = rf.me
	rf.raftState.wUnlock()
	// TODO: send out request vote rpc
	// TODO: 在 send 的函数中也用局部变量计数，如果过半则变成 leader ，如果 state/term 改变则停掉那个线程
	// 函数大概长这样：go send go send go send, ch recv, if ok, ++, 看看是不是变成 leader，if !ok, retry
	var electionMaxTime int
	electionMaxTime = 15 + rand.Intn(15)
	for i := 0; i < electionMaxTime; i++ {
		time.Sleep(time.Duration(loopTimeUnit) * time.Millisecond)
		rf.raftState.rLock()
		// 如果发现已经不是 candidate，则 break
		if rf.raftState.state != candidateState {
			rf.raftState.rUnlock()
			break
		}
		rf.raftState.rUnlock()
	}
	// next election time out or change to other state
}

func (rf *Raft) followerMain() {
	var electionMaxTime int
	electionMaxTime = 15 + rand.Intn(15)
	for i := 0; i < electionMaxTime; i++ {
		time.Sleep(time.Duration(loopTimeUnit) * time.Millisecond)
		rf.raftState.rLock()
		if rf.raftState.resetElectionTimer {
			i = 0
		}
		rf.raftState.rUnlock()
	}
	rf.raftState.wLock()
	rf.raftState.state = candidateState
	rf.electionLog("election time out,  become candidate\n")
	rf.raftState.wUnlock()
}

func (rf *Raft) mainLoop() {
	for !rf.killed() {
		// atomically read state
		rf.raftState.rLock()
		curState := rf.raftState.state
		rf.raftState.rUnlock()
		switch curState {
		case leaderState:
			rf.leaderMain()
			break
		case candidateState:
			rf.doElection()
			break
		case followerState:
			rf.followerMain()
			break
		default:
			log.Fatalln("undefined raft state.")
			break
		}
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
	// TODO: init all status to follower

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	// go rf.ticker()
	go rf.mainLoop()

	return rf
}
