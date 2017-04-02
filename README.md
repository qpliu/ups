ups is a Go package for implementing http microservices using Protocol Buffers.

[![GoDoc](https://godoc.org/github.com/qpliu/ups?status.svg)](https://godoc.org/github.com/qpliu/ups)
[![Build Status](https://travis-ci.org/qpliu/ups.svg?branch=master)](https://travis-ci.org/qpliu/ups)

Protocol Buffers: https://github.com/golang/protobuf

JSON is also supported using https://github.com/golang/protobuf/jsonpb

# Example

```protobuf
syntax = "proto3";

message HelloRequest {
    string name = 1;
}

message HelloResponse {
    string text = 1;
}
```

```go
	http.Handle("/hello", ups.UPS(func(req *HelloRequest) *HelloResponse {
		return &HelloResponse{Text: "Hello, " + req.Name + "!"}
	}))
```
