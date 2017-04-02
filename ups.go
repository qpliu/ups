// Package ups supports implementing http microservices using Protocol Buffers.
package ups

import (
	"bytes"
	"context"
	"log"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
)

var (
	ContextIDKey = contextIDKey{}

	DefaultConfig = Config{
		JSONMarshaler: &jsonpb.Marshaler{OrigName: true},

		LogError: func(ctx context.Context, tag string, err error) {
			log.Printf("[%v] ERROR: %s: %s", ctx.Value(ContextIDKey), tag, err.Error())
		},
		LogPanic: func(ctx context.Context, err interface{}) {
			log.Printf("[%v] PANIC: %v: %s", ctx.Value(ContextIDKey), err, debug.Stack())
		},
		LogStartRequest: func(ctx context.Context, method string, url *url.URL) {
			log.Printf("[%v] %s %s", ctx.Value(ContextIDKey), method, url)
		},
		LogEndRequest: func(ctx context.Context, method string, url *url.URL, statusCode int) {
			log.Printf("[%v] STATUS: %d %s", ctx.Value(ContextIDKey), statusCode, url)
		},
		LogRequestMessage: func(ctx context.Context, req proto.Message) {
			log.Printf("[%v] REQ proto: %s", ctx.Value(ContextIDKey), req.String())
		},
		LogResponseMessage: func(ctx context.Context, resp proto.Message) {
			log.Printf("[%v] RESP proto: %s", ctx.Value(ContextIDKey), resp.String())
		},
		LogRequestBytes: func(ctx context.Context, req []byte) {
			log.Printf("[%v] REQ bytes: %x", ctx.Value(ContextIDKey), req)
		},
		LogResponseBytes: func(ctx context.Context, resp []byte) {
			log.Printf("[%v] RESP bytes: %x", ctx.Value(ContextIDKey), resp)
		},
		LogRequestJSON: func(ctx context.Context, req string) {
			log.Printf("[%v] REQ JSON: %s", ctx.Value(ContextIDKey), req)
		},
		LogResponseJSON: func(ctx context.Context, resp string) {
			log.Printf("[%v] RESP JSON: %s", ctx.Value(ContextIDKey), resp)
		},
	}
)

var (
	messageType = reflect.TypeOf((*proto.Message)(nil)).Elem()
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	requestType = reflect.TypeOf((*http.Request)(nil))

	contextID uint64
)

type handlerType int

const (
	messageHandlerType handlerType = iota
	contextHandlerType
	requestHandlerType
)

type contextIDKey struct {
}

type Config struct {
	JSONMarshaler *jsonpb.Marshaler

	LogError           func(context.Context, string, error)
	LogPanic           func(context.Context, interface{})
	LogStartRequest    func(ctx context.Context, method string, url *url.URL)
	LogEndRequest      func(ctx context.Context, method string, url *url.URL, statusCode int)
	LogRequestMessage  func(context.Context, proto.Message)
	LogResponseMessage func(context.Context, proto.Message)
	LogRequestBytes    func(context.Context, []byte)
	LogResponseBytes   func(context.Context, []byte)
	LogRequestJSON     func(context.Context, string)
	LogResponseJSON    func(context.Context, string)

	ErrorResponse func(ctx context.Context, statusCode int) string
}

// UPS takes a func and creates an http.Handler using the DefaultConfig.
//
// The func must take take one or two arguments and return a single value.
//
// The func must return a proto.Message, which will be marshalled into
// the response.
//
// If the func takes one argument, it must be a proto.Message, which will
// be unmarshalled from the request body.
//
// If the func takes two arguments, the first argument must either be a
// context.Context or a *http.Request, and the second argument must be a
// proto.Message.
//
// UPS will panic if the argument is not a valid func.
func UPS(handler interface{}) http.Handler {
	return UPSWithConfig(handler, DefaultConfig)
}

// UPSWithConfig takes a func and creates an http.Handler using the
// provided Config.
//
// The func must take take one or two arguments and return a single value.
//
// The func must return a proto.Message, which will be marshalled into
// the response.
//
// If the func takes one argument, it must be a proto.Message, which will
// be unmarshalled from the request body.
//
// If the func takes two arguments, the first argument must either be a
// context.Context or a *http.Request, and the second argument must be a
// proto.Message.
//
// UPSWithConfig will panic if the argument is not a valid func.
func UPSWithConfig(handler interface{}, config Config) http.Handler {
	ups := &upsHandler{config: config, handler: reflect.ValueOf(handler)}

	ty := reflect.TypeOf(handler)

	if ty.NumOut() != 1 || !ty.Out(0).Implements(messageType) {
		panic("ups: invalid handler return type")
	}

	var reqType reflect.Type
	switch ty.NumIn() {
	case 1:
		ups.handlerType = messageHandlerType
		reqType = ty.In(0)
	case 2:
		reqType = ty.In(1)
		switch ty.In(0) {
		case contextType:
			ups.handlerType = contextHandlerType
		case requestType:
			ups.handlerType = requestHandlerType
		default:
			panic("ups: invalid handler parameter types")
		}
	default:
		panic("ups: invalid handler parameter types")
	}

	if !reqType.Implements(messageType) {
		panic("ups: invalid handler parameter type")
	}

	ups.requestObjectPool.New = func() interface{} {
		return reflect.New(reqType.Elem())
	}

	return ups
}

type upsHandler struct {
	config            Config
	handlerType       handlerType
	handler           reflect.Value
	requestObjectPool sync.Pool
}

func (ups *upsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctxID := atomic.AddUint64(&contextID, 1)
	ctx := context.WithValue(r.Context(), ContextIDKey, ctxID)
	r = r.WithContext(ctx)

	statusCode := http.StatusOK
	var resp []byte
	func() {
		defer func() {
			if err := recover(); err != nil {
				ups.logPanic(ctx, err)
				statusCode = http.StatusInternalServerError
			}
		}()

		ups.logStartRequest(ctx, r.Method, r.URL)
		if r.Method != http.MethodPost {
			statusCode = http.StatusMethodNotAllowed
			return
		}

		var reqBuffer bytes.Buffer
		if _, err := reqBuffer.ReadFrom(r.Body); err != nil {
			ups.logError(ctx, "req.ReadFrom", err)
			statusCode = http.StatusInternalServerError
			return
		}
		req := reqBuffer.Bytes()

		json := false
		if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil {
			ups.logError(ctx, "mime.ParseMediaType", err)
			statusCode = http.StatusUnsupportedMediaType
			return
		} else {
			switch contentType {
			case "application/json":
				if ups.config.JSONMarshaler == nil {
					statusCode = http.StatusUnsupportedMediaType
					return
				}
				json = true
			case "application/octet-stream", "application/x-protobuf":
				json = false
			default:
				statusCode = http.StatusUnsupportedMediaType
				return
			}
		}

		arg := ups.requestObjectPool.Get().(reflect.Value)
		defer func() {
			arg.Interface().(proto.Message).Reset()
			ups.requestObjectPool.Put(arg)
		}()
		if json {
			ups.logRequestJSON(ctx, string(req))
			if err := jsonpb.Unmarshal(bytes.NewReader(req), arg.Interface().(proto.Message)); err != nil {
				ups.logError(ctx, "jsonpb.Unmarshal", err)
				statusCode = http.StatusInternalServerError
				return
			}
		} else {
			ups.logRequestBytes(ctx, req)
			if err := proto.Unmarshal(req, arg.Interface().(proto.Message)); err != nil {
				ups.logError(ctx, "proto.Unmarshal", err)
				statusCode = http.StatusInternalServerError
				return
			}
		}
		ups.logRequestMessage(ctx, arg.Interface().(proto.Message))

		var args []reflect.Value
		switch ups.handlerType {
		case messageHandlerType:
			args = []reflect.Value{arg}
		case contextHandlerType:
			args = []reflect.Value{reflect.ValueOf(ctx), arg}
		case requestHandlerType:
			args = []reflect.Value{reflect.ValueOf(r), arg}
		}

		result := ups.handler.Call(args)[0].Interface().(proto.Message)
		ups.logResponseMessage(ctx, result)

		if json {
			if response, err := ups.config.JSONMarshaler.MarshalToString(result); err != nil {
				ups.logError(ctx, "JSONMarshaler.MarshalToString", err)
				statusCode = http.StatusInternalServerError
			} else {
				ups.logResponseJSON(ctx, response)
				resp = []byte(response)
				w.Header().Set("Content-Type", "application/json")
			}
		} else {
			if response, err := proto.Marshal(result); err != nil {
				ups.logError(ctx, "proto.Marshal", err)
				statusCode = http.StatusInternalServerError
			} else {
				ups.logResponseBytes(ctx, response)
				resp = response
				w.Header().Set("Content-Type", "application/octet-stream")
			}
		}
	}()

	if statusCode == http.StatusOK {
		for {
			if n, err := w.Write(resp); err != nil {
				ups.logError(ctx, "w.Write", err)
				break
			} else if n >= len(resp) {
				break
			} else {
				resp = resp[n:]
			}
		}
	} else {
		http.Error(w, ups.errorResponse(ctx, statusCode), statusCode)
	}
	ups.logEndRequest(ctx, r.Method, r.URL, statusCode)
}

func (ups *upsHandler) logError(ctx context.Context, tag string, err error) {
	if ups.config.LogError != nil {
		ups.config.LogError(ctx, tag, err)
	}
}

func (ups *upsHandler) logPanic(ctx context.Context, err interface{}) {
	if ups.config.LogPanic != nil {
		ups.config.LogPanic(ctx, err)
	}
}

func (ups *upsHandler) logStartRequest(ctx context.Context, method string, url *url.URL) {
	if ups.config.LogStartRequest != nil {
		ups.config.LogStartRequest(ctx, method, url)
	}
}

func (ups *upsHandler) logEndRequest(ctx context.Context, method string, url *url.URL, statusCode int) {
	if ups.config.LogEndRequest != nil {
		ups.config.LogEndRequest(ctx, method, url, statusCode)
	}
}

func (ups *upsHandler) logRequestMessage(ctx context.Context, req proto.Message) {
	if ups.config.LogRequestMessage != nil {
		ups.config.LogRequestMessage(ctx, req)
	}
}

func (ups *upsHandler) logResponseMessage(ctx context.Context, resp proto.Message) {
	if ups.config.LogResponseMessage != nil {
		ups.config.LogResponseMessage(ctx, resp)
	}
}

func (ups *upsHandler) logRequestBytes(ctx context.Context, req []byte) {
	if ups.config.LogRequestBytes != nil {
		ups.config.LogRequestBytes(ctx, req)
	}
}

func (ups *upsHandler) logResponseBytes(ctx context.Context, resp []byte) {
	if ups.config.LogResponseBytes != nil {
		ups.config.LogResponseBytes(ctx, resp)
	}
}

func (ups *upsHandler) logRequestJSON(ctx context.Context, req string) {
	if ups.config.LogRequestJSON != nil {
		ups.config.LogRequestJSON(ctx, req)
	}
}

func (ups *upsHandler) logResponseJSON(ctx context.Context, resp string) {
	if ups.config.LogResponseJSON != nil {
		ups.config.LogResponseJSON(ctx, resp)
	}
}

func (ups *upsHandler) errorResponse(ctx context.Context, statusCode int) string {
	if ups.config.ErrorResponse != nil {
		return ups.config.ErrorResponse(ctx, statusCode)
	} else {
		return ""
	}
}
