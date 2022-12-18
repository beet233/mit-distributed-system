# lab3-kvraft

## Part 3A - Key/value service without snapshots

我们需要基于 Raft 实现一个容灾备份的 Key-Value 存储，并保证其**线性一致性**。作为这样强一致的存储，自然我们需要给出简单易用的接口，让使用这个存储就像使用一个单机存储一样自然。

用户将通过 client 来访问这个服务，而这个服务由多个 server 组成，这些 server 间的联系由底层的每个 server 对应一个的 raft replica 来维系。

```
-------------                                  ----------         
|     Client   |      --------- \            |  Server |         ----------
|  ---------  |      to leader  >         ----------         |  Server |
|  |  Clerk  |  |      --------- /           ----------          ----------
|  ---------  |                                 |  Server |
-------------                                 ----------
```

> 每个 client 通过一个 `struct Clerk` 来访问 server。

因为要保证线性一致性，所以从副本读是不现实的（提高并发读的能力请参考 ***ZooKeeper***），于是乎所有的读写请求都由 raft leader 对应的 server 来承担。当然，这样的结果是，server 越多，实际上达成一致的代价就越高，速度就更慢，除 leader 以外的 server 只能承担容灾的作用，但这里的优化不在本实验的考虑范围内。