# lab1-mapreduce

## 代码结构

coordinator和worker入口：

```shell
/main/mrcoordinator
/main/mrworker
```

具体实现：

```shell
/mr/coordinator.go
/mr/rpc.go
/mr/worker.go
```

用来跑的应用：

```shell
/mrapps/*
```

## 代码思路

首先启动一个coordinator：

```shell
go run -race mrcoordinator.go pg-*.txt
```

然后多开几个终端，启动一些worker，这里跑一个word count：

```shell
go run -race mrworker.go wc.so
```

### 大致流程

coordinator在初始化时，把要干的任务先分好（简单起见，令一个文本文件为一个任务），启动一个server，接收worker的任务请求。worker向coordinator轮询请求任务。worker既可以做map也可以做reduce，由coordinator下发的任务决定。

map的任务由coordinator设一个任务号，reduce任务则是有nReduce个，每个map任务产出nReduce个临时文件，比如mr-mapId-reduceId，mapId就是coordinator设置的任务号，reduceId在这里是对key（word count里就是word）做一个哈希函数算出来的0~nReduce-1的值。

每个map任务就是不断地从一个文件里读入，产出word : 1这样的pair，以json的形式存入一个个mr-mapId-reduceId-tmp临时文件中，worker做完了之后通知coordinator，coordinator把这个mapId的所有mr-mapId-reduceId-tmp文件改成mr-mapId-reduceId，表示是已经完成的（如果超过了ddl或者crash导致通知失败，那么这些带-tmp的文件就视为无效了，coordinator把挂了的任务重新分配给别的worker，并把之前的-tmp文件删掉）。

等到所有map任务都结束了，开始reduce。每个reduce任务处理自己reduceId的所有mr-mapId-reduceId文件，把它们的结果聚合到mr-out-reduceId-tmp文件里，然后通知coordinator，通知同上，超过ddl则失效，没超过则让coordinator把-tmp去掉，成为正式的结果。

所有reduce任务结束后，coordinator关闭，worker们请求不到了也就自己去死了。

### 各种标记的设计

```go
type Coordinator struct {
	NMap             int
	NReduce          int
	IsMapFinished    bool
	IsFinished       bool
	MapTaskList      []MapTask
	ReduceTaskList   []ReduceTask
	MapAssignLock    sync.Mutex
	ReduceAssignLock sync.Mutex
}
```

Map任务数，Reduce任务数，结束标志，任务列表，分配任务的锁。

```go
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
```

任务情况：Phase记录了这个任务的当前状况。TaskNumber记录这个任务被派发的次数（从第0次开始）。mux是任务上的锁，避免这个任务被同时修改，产生有风险的data race。

如果一个任务发出去，ddl内没回来，这个任务被重新派给了另一个人，结果老的那个人过了一会先返回了，那我怎么知道这是新的返回还是老的返回？老的返回应该被判定为无效。

我的策略：派发任务时，coordinator同时发出一个taskNumber，worker完成后也把这个taskNumber传回来。每次ddl到达时coordinator处记录的taskNumber递增。只有回来的号和现在的号相符，才是正确的结果，否则无视。

RPC传参和返回：

```go
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
```

Args:

> AskForTask，是否是来找新任务的
>
> IsMap，如果是来报道任务完成的，是map还是reduce，用到下面的MapTaskId或ReduceTaskId

Reply:

> IsFinished，是不是全部完成了，可以关闭了
>
> IsMap，分配的是map还是reduce

### 锁的设计和使用

+ 上一条说的ddl处理和接收到worker的返回，这两者是有冲突的，不能让它们同时开始，否则错误五花八门。需要加锁，设计为加在单个任务上的锁。
+ 分配任务时，map和reduce分别用一个coordinator的全局锁，防止分出去同样的任务。

### DDL实现

```go
// 派发任务后启动一个线程
go c.MapTaskChecker(reply.MapTaskId)
```

```go
func (c *Coordinator) MapTaskChecker(MapTaskId int) {
    // 设置ddl时长
	time.Sleep(time.Second * 30)
	c.MapTaskList[MapTaskId].mux.Lock()
	if c.MapTaskList[MapTaskId].Phase != 1 {
		c.MapTaskList[MapTaskId].SetPhase(-1)
		c.MapTaskList[MapTaskId].TaskNumber++
	}
	c.MapTaskList[MapTaskId].mux.Unlock()
}
```

## 实现效果与测试

```shell
$ ./test-mr.sh
*** Starting wc test.
--- wc test: PASS
*** Starting indexer test.
--- indexer test: PASS
*** Starting map parallelism test.
--- map parallelism test: PASS
*** Starting reduce parallelism test.
--- reduce parallelism test: PASS
*** Starting job count test.
--- map jobs ran incorrect number of times (11 != 8)
--- job count test: FAIL
*** Starting early exit test.
--- early exit test: PASS
*** Starting crash test.
--- crash test: PASS
*** FAILED SOME TESTS
```

为什么job count挂了捏？

> job count这个测试的目的是检查在没有failure的情况下，一个map任务是否会被派发多次。在我的逻辑中，这是不可能的。那为什么挂呢？理论上map的任务个数等于输入的文件个数，这里是8，最后结果却比8大，经过仔细的检查，我确实每个任务只派发了一次（在没有失败的情况下），但是jobcount.go中是依靠统计文件夹内的文件来判定任务次数的，而当文件夹正在被写时，ioutil.ReadDir可能会发生错乱（我发现了同一个文件被计入两次的情况），如果要解决，可以通过加一个directory.lock锁文件的方式，每次修改/读取文件夹内文件时，如果锁文件已经存在，阻塞，如果不存在，创建锁文件，任务结束后删除锁文件。（用锁文件的原因是分布式系统中内存不共享，我们需要一个能共享的锁，那就是本地存储）

并不影响其他计算型测试的正确性。