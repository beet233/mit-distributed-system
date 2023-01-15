# Amazon Aurora 架构分析

## Intro

Amazon Aurora is a relational database service for OLTP workloads offered as part of Amazon Web Services (AWS).

> |          | OLTP（On-line Transaction Processing） | OLAP（On-Line Analytical Processing）   |
> | -------- | -------------------------------------- | --------------------------------------- |
> | 用户     | 操作人员，低层管理人员                 | 决策人员，高级管理人员                  |
> | 功能     | 日常操作处理                           | 分析决策                                |
> | DB 设计  | 面向应用                               | 面向主题                                |
> | 数据     | 当前的， 最新的细节的， 二维的分立的   | 历史的， 聚集的， 多维的集成的， 统一的 |
> | 存取     | 读/写数十条记录                        | 读上百万条记录                          |
> | 工作单位 | 简单的事务                             | 复杂的查询                              |
> | DB 大小  | 100MB-GB                               | 100GB-TB                                |

相关论文 ***Amazon Aurora: Design Considerations for High Throughput Cloud-Native Relational Databases*** 发表在 `SIGMOID 2017`。

![img](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/202301160024961.jpeg)

这是 Aurora 的总体架构。其中 RDS 指 Relational Database Service。Primary RW DB 是主数据库，读写请求都可以经过，也只有一个。Secondary RO DB 是只读数据库，只处理读请求，可以有多个。但这个架构最大的特点在于，数据并不在 RW DB 或 RO DB 手中，他们只负责接收请求，RW DB 负责产生 Log。Log 传递到 RO DB 作备份以及下面的六个存储节点。存储节点不仅存储数据，将 Log 实际应用到 data page 也由它们完成，意义在于减少网络传输。至于 S3 是一个备份快照的亚马逊服务 Amazon Simple Storage Service，看成更多一层备份就行。

## Build History

### Easy Replica

首先为了让数据不是单份而是至少双份，亚麻搞了个 EBS （Elastic Block Store）。其实就是一对互为副本的存储服务器。它们之间采用 Chain Replication。

![image-20230116004425253](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/202301160044281.png)

但是这还远远不够，首先如果你在 EBS 上简简单单跑一个 MySQL，那这个 Copy 的代价会很大（不仅要传输 Log ，还可能要传输 Data Page，具体原因我也不太清楚）。

另外，亚麻把一个 EBS（本体+mirror）跑在一个数据中心（论文称之为 AZ（Available Zone）），这样也很有道理，毕竟一个数据中心可以直接用高速网线相连，传输速度很快很稳定。但是如果这个数据中心炸了，那就两份一起没有了。

### Backup in Different Area

为了让一个数据中心可以爆炸而不损失数据，所以我们需要把数据放到多个数据中心。

![img](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/202301160054196.jpeg)

上述的 EBS 并没有被抛弃，而是每个数据中心跑一个 EBS 对。

其中，第1,3,5步是串行的，也就是说，只有第1步完成了，才能执行第3步，第3步完成了才能执行第5步。这无疑增加了服务器返回数据的延迟。另外传统的 MySQL 在写入和传输数据时还需要很多的额外信息，这又增加了网络带宽的消耗。也就是说，MySQL 的使用在分布式系统产生了两个问题：

1. 应答延迟太高。
2. 消耗网络带宽太多。

### Only Log

![img](https://beetpic.oss-cn-hangzhou.aliyuncs.com/img/202301160101605.png)

亚麻抛弃了 MySQL 和 EBS，从头到尾自己重新定制。每个数据中心放一个 Instance，每个数据中心放两个存储节点，但不是以 EBS 的形式组织。这里的存储节点不再是通用（General-Purpose）存储，这是一个可以理解MySQL Log条目的存储系统。EBS是一个非常通用的存储系统，它模拟了磁盘，只需要支持读写数据块。EBS不理解除了数据块以外的其他任何事物。而这里的存储系统理解使用它的数据库的Log。所以这里，Aurora将通用的存储去掉了，取而代之的是一个应用定制的（Application-Specific）存储系统。

### Quorum

然而，我们看到图中，一次写请求要告诉整整六个节点，比 Mirrored MySQL 那幅图还要多，虽然只需要传 Log 减少了网络传输，但万一六个节点里某个处理得比较慢/某个 AZ 离得太远，那也会带来高延迟的 Reply。