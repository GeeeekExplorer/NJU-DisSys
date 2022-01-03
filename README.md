# NJU-DisSys

南京大学分布式系统Raft算法，本项目力求最简洁和最直观的实现，完整代码以及必要注释不超过350行，并能通过500小时循环测试。

代码将在实验截止后开源，README已经给出大致框架，只需在注释中补充[论文](https://raft.github.io/raft.pdf)第四页内容即可。

## 运行说明

![](asset/run.png)

## 数据结构

`Raft`结构在论文基础上添加了如下成员：

```go
type Raft struct {
    // fields in the paper
    role  int
    votes int
    timer *time.Timer
}
```

其他结构与论文描述一致。

一些常量：

```go
const (
    FOLLOWER  = 0
    CANDIDATE = 1
    LEADER    = 2
)

const (
    electionTimeoutMin = 300 * time.Millisecond
    electionTimeoutMax = 600 * time.Millisecond
    heartbeatTimeout   = 50 * time.Millisecond
    checkTimeout       = 5 * time.Millisecond
)
```

两个实用函数：
```go
func randTime() time.Duration {
    diff := (electionTimeoutMax - electionTimeoutMin).Milliseconds()
    return electionTimeoutMin + time.Duration(rand.Intn(int(diff)))*time.Millisecond
}

func wait(n int, ch chan bool) {
    for i := 1; i < n; i++ {
        select {
        case <-ch:
        case <-time.After(checkTimeout):
            return
        }
    }
}
```

## 模块设计

Raft算法包含两种RPC，本项目将两种处理流程尽可能统一。

定义`RPC`（具体是`RequestVote`或`AppendEntries`）相关结构：

```go
type RPCArgs struct {
    // fields in the paper
}

type RPCReply struct {
    // fields in the paper
}
```

接收者响应逻辑：

```go
func (rf *Raft) RPC(args RPCArgs, reply *RPCReply) {
    // receiver responds to the request
    // e.g. convert to follower if outdated
    // or vote for candidate
    // or update logs
}
```

发送者单次请求逻辑，包含可以**立即处理响应**的部分逻辑，通过`channel`发送响应成功：

```go
func (rf *Raft) sendRPC(server int, args RPCArgs, reply *RPCReply, ch chan bool) {
    if !rf.peers[server].Call("Raft.RPC", args, reply) {
        return
    }
    // handle the response immediately
    // e.g. convert to follower and return if outdated
    // or increase the candidate's votes
    // or update the leader's nextIndex
    // finally execute ch <- true
}
```

发送者批量请求逻辑，等待所有发收结束或者超时，之后处理剩余事务（竞选成功或日志提交）：

```go
ch := make(chan bool)
for i := 0; i < n; i++ {
    if i != rf.me {
        // construct args and reply
        go rf.sendRPC(i, args, &reply, ch)
    }
}

// wait all goroutines go well or time out
wait(n, ch)

// handle the remaining transactions
// e.g. decide whether to become a leader
// or find the appropriate index to commit
```

在`Make()`函数中两个无限循环的`goroutine`，一个定时检查日志应用情况，另一个计时器触发对于`FOLLOWER`只需变为`CANDIDATE`，对于`CANDIDATE`或`LEADER`执行上面代码逻辑：

```go
go func() {
    for rf.me != -1 {
        time.Sleep(checkTimeout)
        // check whether to apply log to state machine
    }
}()

go func() {
    for rf.me != -1 {
        <-rf.timer.C
        switch rf.role {
        case FOLLOWER:
            // become candidate
        case CANDIDATE:
            // run for election
        case LEADER:
            // manage log replication
        }
    }
}()
```

在`Kill()`函数中执行`rf.me = -1`结束上面两个`goroutine`。

## 注意事项

1. 并发执行数据访问常加锁

2. 修改非易失成员立即持久化

## 鲁棒测试

成功通过一次测试并不是终点，多次运行仍然可能出错，可以在`shell`中定义如下函数：

```shell
run() {
  go test
  while [[ $? -ne 1 ]]
  do
    go test
  done
}
```

执行`run`命令无限测试直至失败，本项目通过了500小时的循环测试。
