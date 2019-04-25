# krakend-lambda

consume lambda functions through a KrakenD API Gateway

## Local test

Set up the localstack container

```
docker run --rm --name localstack -it \
  -p 4567-4583:4567-4583 \
  -p 8080:8080 \
  -e HOSTNAME_EXTERNAL=$(docker-machine ip localstack) \
  -e SERVICES=serverless \
  -e DEFAULT_REGION=us-east-1 \
  -e LAMBDA_EXECUTOR=local \
  -e DATA_DIR=/tmp/localstack/data \
  -v /tmp/localstack:/tmp/localstack \
  localstack/localstack
```

build and zip your sample lambdas

```
$ cd ./test/lambda1 && zip lambda1.zip handler.py && cd ../../ && mv ./test/lambda1/lambda1.zip .
```

register your lambda

```
$ aws --endpoint-url=http://$(docker-machine ip localstack):4574 --no-verify-ssl \
	lambda create-function \
	    --region us-east-1 \
	    --memory-size 128 \
	    --zip-file fileb://lambda1.zip \
	    --role arn:aws:iam::123456:role/irrelevant \
	    --function-name python37 \
	    --runtime python37 \
	    --handler handler.my_handler
```

test it

```
aws  --endpoint-url=http://$(docker-machine ip localstack):4574 --no-verify-ssl \
    lambda invoke \
        --function-name python37 \
        --invocation-type "RequestResponse" \
        --payload '{"first_name":"foo","last_name":"bar"}' \
        response.txt
```

test the backend

```
cd test
go test
```