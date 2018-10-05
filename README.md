[![Build Status](https://travis-ci.org/kaihendry/prazespeed.svg?branch=master)](https://travis-ci.org/kaihendry/prazespeed)

# Environment variables

Maintained via [up](https://up.docs.apex.sh/)

	eval "$(up env export)"

# GetMetricWidgetImage cost

<img src="https://s.natalian.org/2018-10-04/1538621202_1406x1406.png">

# GetMetricWidgetImage API

I am using the v2 Golang AWS SDK since I find the authentication stuff easier.
However it seems a bit laggard for
[GetMetricWidgetImage](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/CloudWatch-Metric-Widget-Structure.html)
models and such. :/

https://github.com/aws/aws-sdk-go-v2/issues/223#issuecomment-427084745
