package mr

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"time"
)
import "log"
import "net/rpc"
import "hash/fnv"

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for {
		time.Sleep(time.Microsecond * 200)
		args := Args{}
		args.AskForTask = true

		reply := Reply{}

		ok := call("Coordinator.RPCHandler", &args, &reply)
		if ok {
			//fmt.Printf("call success!\n")
			if reply.IsFinished {
				return
			} else {
				if reply.IsMap {
					if reply.MapTaskId >= 0 {
						// map把所有的统计结果放进mr-mapid-reduceid这样的文件里
						file, err := os.Open(reply.MapFileName)
						if err != nil {
							log.Fatalf("cannot open %v", reply.MapFileName)
						}
						content, err := ioutil.ReadAll(file)
						if err != nil {
							log.Fatalf("cannot read %v", reply.MapFileName)
						}
						file.Close()
						keyValueList := mapf(reply.MapFileName, string(content))
						middleResult := [][]KeyValue{}
						for i := 0; i < reply.NReduce; i++ {
							middleResult = append(middleResult, []KeyValue{})
						}
						for _, keyValue := range keyValueList {
							middleResult[ihash(keyValue.Key)%reply.NReduce] =
								append(middleResult[ihash(keyValue.Key)%reply.NReduce], keyValue)
						}
						// 把middleResult存进文件
						for i := 0; i < reply.NReduce; i++ {
							oname := fmt.Sprintf("mr-%v-%v-tmp", reply.MapTaskId, i)
							os.Remove(oname)
							ofile, _ := os.Create(oname)
							enc := json.NewEncoder(ofile)
							for _, keyValue := range middleResult[i] {
								err := enc.Encode(&keyValue)
								if err != nil {
									log.Fatalf("json encoding failed")
								}
							}
							ofile.Close()
						}

						// 做完了之后通知一下coordinator
						innerArgs := Args{}
						innerArgs.AskForTask = false
						innerArgs.IsMap = true
						innerArgs.MapTaskId = reply.MapTaskId
						innerArgs.TaskNumber = reply.TaskNumber
						innerReply := Reply{}
						call("Coordinator.RPCHandler", &innerArgs, &innerReply)
					}
				} else {
					if reply.ReduceTaskId >= 0 {
						// 这里需要把所有这个reduceid的文件读取，总合成mr-out-reduceid
						reduceResult := []KeyValue{}
						for i := 0; i < reply.NMap; i++ {
							filename := fmt.Sprintf("mr-%v-%v", i, reply.ReduceTaskId)
							file, _ := os.Open(filename)
							dec := json.NewDecoder(file)
							for {
								var kv KeyValue
								if err := dec.Decode(&kv); err != nil {
									break
								}
								reduceResult = append(reduceResult, kv)
							}
						}

						// 将reduceResult存进mr-out-reduceid
						oname := fmt.Sprintf("mr-out-%v-tmp", reply.ReduceTaskId)
						os.Remove(oname)
						ofile, _ := os.Create(oname)
						sort.Sort(ByKey(reduceResult))
						i := 0
						for i < len(reduceResult) {
							j := i + 1
							for j < len(reduceResult) && reduceResult[j].Key == reduceResult[i].Key {
								j++
							}
							values := []string{}
							for k := i; k < j; k++ {
								values = append(values, reduceResult[k].Value)
							}
							output := reducef(reduceResult[i].Key, values)

							fmt.Fprintf(ofile, "%v %v\n", reduceResult[i].Key, output)

							i = j
						}
						ofile.Close()
						// 做完了之后通知一下coordinator
						innerArgs := Args{}
						innerArgs.AskForTask = false
						innerArgs.IsMap = false
						innerArgs.ReduceTaskId = reply.ReduceTaskId
						innerArgs.TaskNumber = reply.TaskNumber
						innerReply := Reply{}
						call("Coordinator.RPCHandler", &innerArgs, &innerReply)
					}
				}
			}
		} else {
			fmt.Printf("call failed!\n")
		}
	}

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
