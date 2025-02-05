# Benchmarking Barco Streams

We tested Barco Streams performance on different AWS instance types to provide
detailed information on what to expect in terms of throughput and latency for given hardware specs. The goal is not
to push the upper limits of Barco as it's still in early development but to define the direction it's heading in terms
of performance.

The workload is designed to post 1 KiB messages containing JSON data. The message is composed by a large portion of
random data alongside dictionary values, numbers and UUIDs to try to represent real world data. Barco Streams uses
**compression by default** so the shape of the data matters as it will affect the amount of data flushed to disk.
Barco **writes are durable** by sending data to two replicas and waiting for a replica acknowledgement before
responding to the client.

The tool used to benchmark Barco is [available on GitHub][tool-repo] and it can be used to reproduce these results.

The [AWS instance types][instance-types] selected are both arm64 and amd64 instances with EBS-only storage. Using EBS
storage is common pattern and, even though it can be slower than attached NVMe SSDs, it's the most sensible option
when deploying a stateful service in the Cloud due to costs and durability guarantees:

- **t4g.micro**: One of the cheapest instances on AWS costing less than a cent per hour. 2 vCPU (burstable
with 10% baseline performance) and 1 GiB of memory. On demand cost is $0.0084 per hour.
- **c7g.medium**: 1 vCPU (Arm-based AWS Graviton3 processors) and 1 GiB of memory. On demand cost is $0.0361 per hour.
- **c6i.large**: 2 vCPU (Up to 3.5 GHz 3rd generation Intel Xeon Scalable processors) and 4 GiB of memory.
On demand cost is $0.085 per hour.
- **c6i.xlarge**: 4 vCPU (Up to 3.5 GHz 3rd generation Intel Xeon Scalable processors) and 8 GiB of memory.
On demand cost is $0.17 per hour.

<p align="center">
    <img src="https://user-images.githubusercontent.com/2931196/195530734-c3738cfc-b989-476b-b9d2-4fd87b86652d.png" alt="Throughput by instance type">
    <br>
    <em>Messages per second by instance</em>
</p>

The results show that Barco can process writes up to 1.4M messages/sec (1.37 GiB/s) on a commodity cluster
composed of 3 `c6i.xlarge` brokers. Even on the cheapest C7g instance with one vCPU it can achieve more than
400K msgs/s. The max latency on all runs was under 100ms.

What we find specially interesting is that **Barco can support writes of more than 50K msgs/s with baseline CPU
performance of `t4g.micro`, bursting up to 537K msgs/s**. This is also an example of what resource sharing might
look like when running Barco on Kubernetes with a wide [requests-limits resource range][k8s-resource-mgmt].

The commit hash used for testing was [ddcc496](https://github.com/barcostreams/barco/commit/ddcc496f516c9343dcdb876b6692f0659c6a787e)
(`v0.2.0`).

## Capacity planning compared to Apache Kafka

Using the benchmark results from above can help us do capacity planning for a production deployment to answer the
question what hardware will I need to support a certain volume of data.

To provide a good baseline, we compare the computing cost of running Barco to Apache Kafka. We propose two scenarios:
1. Deploy Apache Kafka according to [Confluent system requirements][confluent-system] (only brokers, no costs for
Zookeeper, Connect, ...)
2. Deploy Apache Kafka with minimal hardware requirements.

For the first scenario, we use 3 `m5.4xlarge` brokers (64 GiB memory) and we state that Kafka can support
500K msgs/s with those instances with EBS storage, while keeping max latency under 100ms.
For the latter, we use `m5.xlarge` instances with 16 GiB of memory and define target throughput to 250K msgs/s with
predictable latency.

Rates are in USD and represent the yearly computing costs of running Barco Streams brokers compared to
Apache Kafka brokers.

<p align="center">
    <img src="https://user-images.githubusercontent.com/2931196/195530721-6efb539d-f31d-4abd-b1ef-0fd2200c7d0e.png" style="margin: 0 auto">
    <br>
    <em>Cost of running Barco and Kafka for a target throughput based on Confluent system requirements</em>
</p>

<p>&nbsp;</p>

<p align="center">
    <img src="https://user-images.githubusercontent.com/2931196/195530728-3ffeaf76-d961-449a-a32b-ef38a7ec7054.png" style="margin: 0 auto">
    <br>
    <em>Cost of running Barco and Kafka for a target throughput (minimal H/W)</em>
</p>

[instance-types]: https://aws.amazon.com/ec2/instance-types/
[tool-repo]: https://github.com/jorgebay/barco-benchmark-tool/
[confluent-system]: https://docs.confluent.io/platform/current/installation/system-requirements.html#confluent-system-requirements
[k8s-resource-mgmt]: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
