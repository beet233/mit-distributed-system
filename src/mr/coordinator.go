package mr

import (
	"fmt"
	"log"
	"sync"
	"time"
)
import "net"
import "os"
import "net/rpc"
import "net/http"

type MapTask struct {
	// -1: 未分配 0:已分配，运行中 1:已分配，已完结
	Phase      int
	FileName   string
	TaskNumber int
	mux        sync.Mutex
}

type ReduceTask struct {
	// -1: 未分配 0:已分配，运行中 1:已分配，已完结
	Phase      int
	TaskNumber int
	mux        sync.Mutex
}

func (mapTask *MapTask) SetPhase(phase int) {
	//mapTask.mux.Lock()
	mapTask.Phase = phase
	//mapTask.mux.Unlock()
}

func (reduceTask *ReduceTask) SetPhase(phase int) {
	//reduceTask.mux.Lock()
	reduceTask.Phase = phase
	//reduceTask.mux.Unlock()
}

type Coordinator struct {
	// Your definitions here.
	NMap             int
	NReduce          int
	IsMapFinished    bool
	IsFinished       bool
	MapTaskList      []MapTask
	ReduceTaskList   []ReduceTask
	MapAssignLock    sync.Mutex
	ReduceAssignLock sync.Mutex
}

// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

func (c *Coordinator) GetUnassignedMapTask() int {
	MapTaskId := -1
	for index, value := range c.MapTaskList {
		if value.Phase == -1 {
			MapTaskId = index
			break
		}
	}
	return MapTaskId
}

func (c *Coordinator) GetUnassignedReduceTask() int {
	ReduceTaskId := -1
	for index, value := range c.ReduceTaskList {
		if value.Phase == -1 {
			ReduceTaskId = index
			break
		}
	}
	return ReduceTaskId
}

func (c *Coordinator) MapTaskChecker(MapTaskId int) {
	time.Sleep(time.Second * 30)
	c.MapTaskList[MapTaskId].mux.Lock()
	if c.MapTaskList[MapTaskId].Phase != 1 {
		c.MapTaskList[MapTaskId].SetPhase(-1)
		c.MapTaskList[MapTaskId].TaskNumber++
	}
	c.MapTaskList[MapTaskId].mux.Unlock()
}

func (c *Coordinator) ReduceTaskChecker(ReduceTaskId int) {
	time.Sleep(time.Second * 30)
	c.ReduceTaskList[ReduceTaskId].mux.Lock()
	if c.ReduceTaskList[ReduceTaskId].Phase != 1 {
		c.ReduceTaskList[ReduceTaskId].SetPhase(-1)
		c.ReduceTaskList[ReduceTaskId].TaskNumber++
	}
	c.ReduceTaskList[ReduceTaskId].mux.Unlock()
}

func (c *Coordinator) RPCHandler(args *Args, reply *Reply) error {
	reply.NMap = c.NMap
	reply.NReduce = c.NReduce
	if args.AskForTask {
		if c.IsFinished {
			// 告诉worker所有任务已经结束，可以洗洗睡了
			reply.IsFinished = true
		} else {
			reply.IsFinished = false
			if c.IsMapFinished {
				reply.IsMap = false
				// 为了防止给俩同时请求的worker发了同样的task，这里是需要加个锁的
				c.ReduceAssignLock.Lock()
				reply.ReduceTaskId = c.GetUnassignedReduceTask()
				if reply.ReduceTaskId >= 0 {
					// 这里不需要加锁，因为获得的是phase为-1的task，要么压根没开始过，要么已经被废弃了，老任务就算回来也是不会被处理的
					//fmt.Printf("派发reduce任务%v\n", reply.ReduceTaskId)
					c.ReduceTaskList[reply.ReduceTaskId].SetPhase(0)
					reply.TaskNumber = c.ReduceTaskList[reply.ReduceTaskId].TaskNumber
					// 派发任务后加一个计时器，10s后如果phase还没有变成1，判定失败，恢复成-1
					go c.ReduceTaskChecker(reply.ReduceTaskId)
				}
				c.ReduceAssignLock.Unlock()
			} else {
				reply.IsFinished = c.IsFinished
				reply.IsMap = true
				c.MapAssignLock.Lock()
				reply.MapTaskId = c.GetUnassignedMapTask()
				if reply.MapTaskId >= 0 {
					//fmt.Printf("派发map任务%v\n", reply.MapTaskId)
					c.MapTaskList[reply.MapTaskId].SetPhase(0)
					reply.MapFileName = c.MapTaskList[reply.MapTaskId].FileName
					reply.TaskNumber = c.MapTaskList[reply.MapTaskId].TaskNumber
					// 派发任务后加一个计时器，10s后如果phase还没有变成1，判定失败，恢复成-1
					go c.MapTaskChecker(reply.MapTaskId)
				}
				c.MapAssignLock.Unlock()
			}
		}
	} else if args.IsMap {
		// 报道map任务的结束
		// 先检查是不是最新派发的，如果是已经被判定过期的任务，失效
		c.MapTaskList[args.MapTaskId].mux.Lock()
		if args.TaskNumber != c.MapTaskList[args.MapTaskId].TaskNumber {
			fmt.Printf("map task %v's %v time return too late, treat it as died\n", args.MapTaskId, args.TaskNumber)
		} else {
			// 把所有tmp文件换成正式文件
			for i := 0; i < c.NReduce; i++ {
				filename := fmt.Sprintf("mr-%v-%v-tmp", args.MapTaskId, i)
				newFilename := fmt.Sprintf("mr-%v-%v", args.MapTaskId, i)
				err := os.Rename(filename, newFilename)
				if err != nil {
					log.Fatalf("cannot rename %v", filename)
				}
			}
			c.MapTaskList[args.MapTaskId].SetPhase(1)
			isMapFinished := true
			for _, mapTask := range c.MapTaskList {
				if mapTask.Phase != 1 {
					isMapFinished = false
					break
				}
			}
			c.IsMapFinished = isMapFinished
		}
		c.MapTaskList[args.MapTaskId].mux.Unlock()
	} else {
		c.ReduceTaskList[args.ReduceTaskId].mux.Lock()
		if args.TaskNumber != c.ReduceTaskList[args.ReduceTaskId].TaskNumber {
			fmt.Printf("reduce task %v's %v time return too late, treat it as died\n", args.ReduceTaskId, args.TaskNumber)
		} else {
			// 把tmp文件换成正式文件
			filename := fmt.Sprintf("mr-out-%v-tmp", args.ReduceTaskId)
			newFilename := fmt.Sprintf("mr-out-%v", args.ReduceTaskId)
			err := os.Rename(filename, newFilename)
			if err != nil {
				log.Fatalf("cannot rename %v", filename)
			}
			c.ReduceTaskList[args.ReduceTaskId].SetPhase(1)
			c.IsFinished = c.Done()
		}
		c.ReduceTaskList[args.ReduceTaskId].mux.Unlock()
	}
	return nil
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	ret := true

	// Your code here.
	for _, reduceTask := range c.ReduceTaskList {
		if reduceTask.Phase != 1 {
			ret = false
			break
		}
	}

	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}
	c.NReduce = nReduce
	c.NMap = len(files)
	// Your code here.
	for i := 0; i < nReduce; i++ {
		reduceTask := ReduceTask{-1, 0, sync.Mutex{}}
		c.ReduceTaskList = append(c.ReduceTaskList, reduceTask)
	}

	for _, value := range files {
		mapTask := MapTask{-1, value, 0, sync.Mutex{}}
		c.MapTaskList = append(c.MapTaskList, mapTask)
	}

	c.server()
	return &c
}
