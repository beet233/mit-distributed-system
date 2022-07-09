package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import "os"
import "strconv"

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// Add your RPC definitions here.

// 如果不是ask for task的，那么就是来汇报任务完成的
// 如果ismap，那么就是汇报maptaskid完成，否则就是reducetaskid完成
type Args struct {
	AskForTask   bool
	IsMap        bool
	MapTaskId    int
	ReduceTaskId int
	TaskNumber   int
}

type Reply struct {
	IsFinished   bool
	IsMap        bool
	MapFileName  string
	MapTaskId    int
	ReduceTaskId int
	NMap         int
	NReduce      int
	TaskNumber   int
}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	//fmt.Println(s)
	return s
}
