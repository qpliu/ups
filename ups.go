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

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
)

var (
	DefaultConfig = Config{
		JSONMarshaler: &jsonpb.Marshaler{OrigName: true},

		LogError: func(ctx context.Context, tag string, err error) {
			log.Printf("ERROR: %s: %s", tag, err.Error())
		},
		LogPanic: func(ctx context.Context, err interface{}) {
			log.Printf("PANIC: %v: %s", err, debug.Stack())
		},
		LogStartRequest: func(ctx context.Context, method string, url *url.URL) {
			log.Printf("%s %s", method, url)
		},
		LogEndRequest: func(ctx context.Context, method string, url *url.URL, statusCode int) {
			log.Printf("STATUS: %d %s", statusCode, url)
		},
		LogRequestMessage: func(ctx context.Context, req proto.Message) {
			log.Printf("REQ proto: %s", req.String())
		},
		LogResponseMessage: func(ctx context.Context, resp proto.Message) {
			log.Printf("RESP proto: %s", resp.String())
		},
		LogRequestBytes: func(ctx context.Context, req []byte) {
			log.Printf("REQ bytes: %x", req)
		},
		LogResponseBytes: func(ctx context.Context, resp []byte) {
			log.Printf("RESP bytes: %x", resp)
		},
		LogRequestJSON: func(ctx context.Context, req string) {
			log.Printf("REQ JSON: %s", req)
		},
		LogResponseJSON: func(ctx context.Context, resp string) {
			log.Printf("RESP JSON: %s", resp)
		},
	}
)

var (
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	messageType = reflect.TypeOf((*proto.Message)(nil)).Elem()
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	requestType = reflect.TypeOf((*http.Request)(nil))
)

type handlerType int

const (
	messageHandlerType handlerType = iota
	contextHandlerType
	requestHandlerType
	paramHandlerType
	contextParamHandlerType
	requestParamHandlerType
)

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

// StatusCoder can be implemented by the error returned by a handler,
// in which case it provides the HTTP status code of the response.
type StatusCoder interface {
	StatusCode() int
}

// UPS takes a func and creates an http.Handler using the DefaultConfig.
//
// The func must take take one or two arguments and return one or two
// values.
//
// The func must return a proto.Message, which will be marshalled into
// the response, or return a (proto.Message, error).  If the error is not
// nil, the response will be 500 HTTP status unless the error implements
// StatusCoder, in which case it will provide the HTTP status of the
// response.
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
	return UPSWithParameterAndConfig(handler, nil, DefaultConfig)
}

// UPSWithConfig takes a func and creates an http.Handler using the
// provided Config.
//
// The func must take take one or two arguments and return one or two
// values.
//
// The func must return a proto.Message, which will be marshalled into
// the response, or return a (proto.Message, error).  If the error is not
// nil, the response will be 500 HTTP status unless the error implements
// StatusCoder, in which case it will provide the HTTP status of the
// response.
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
	return UPSWithParameterAndConfig(handler, nil, config)
}

// UPSWithParameter takes a func and creates an http.Handler using the
// DefaultConfig.
//
// The func must take take two or three arguments and return one or two
// values.
//
// The func must return a proto.Message, which will be marshalled into
// the response, or return a (proto.Message, error).  If the error is not
// nil, the response will be 500 HTTP status unless the error implements
// StatusCoder, in which case it will provide the HTTP status of the
// response.
//
// If the func takes two arguments,  The first argument will be the parameter
// passed to UPSWithParameter.  The second argument must be a proto.Message,
// which will be unmarshalled from the request body.
//
// If the func takes three arguments, the first argument must either be a
// context.Context or a *http.Request, the secon argument will be the
// parameter passed to UPSWithParameter, and the third argument must be a
// proto.Message.
//
// UPSWithParameter will panic if the argument is not a valid func.
func UPSWithParameter(handler interface{}, parameter interface{}) http.Handler {
	return UPSWithParameterAndConfig(handler, parameter, DefaultConfig)
}

// UPSWithParameterAndConfig takes a func and creates an http.Handler using
// the provided Config.
//
// The func must take take two or three arguments and return one or two
// values.
//
// The func must return a proto.Message, which will be marshalled into
// the response, or return a (proto.Message, error).  If the error is not
// nil, the response will be 500 HTTP status unless the error implements
// StatusCoder, in which case it will provide the HTTP status of the
// response.
//
// If the func takes two arguments,  The first argument will be the parameter
// passed to UPSWithParameterAndConfig.  The second argument must be a
// proto.Message, which will be unmarshalled from the request body.
//
// If the func takes three arguments, the first argument must either be a
// context.Context or a *http.Request, the secon argument will be the
// parameter passed to UPSWithParameter, and the third argument must be a
// proto.Message.
//
// UPSWithParameterAndConfig will panic if the argument is not a valid func.
func UPSWithParameterAndConfig(handler interface{}, parameter interface{}, config Config) http.Handler {
	ups := &upsHandler{
		config:    config,
		parameter: reflect.ValueOf(parameter),
		handler:   reflect.ValueOf(handler),
	}

	ty := reflect.TypeOf(handler)

	switch ty.NumOut() {
	case 2:
		if !ty.Out(1).Implements(errorType) {
			panic("ups: invalid handler error return type")
		}
		fallthrough
	case 1:
		if !ty.Out(0).Implements(messageType) {
			panic("ups: invalid handler message return type")
		}
	default:
		panic("ups: invalid handler return type")
	}

	var reqType reflect.Type
	var paramType reflect.Type
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
			ups.handlerType = paramHandlerType
			paramType = ty.In(0)
		}
	case 3:
		reqType = ty.In(2)
		switch ty.In(0) {
		case contextType:
			ups.handlerType = contextParamHandlerType
			paramType = ty.In(1)
		case requestType:
			ups.handlerType = requestParamHandlerType
			paramType = ty.In(1)
		default:
			panic("ups: invalid handler parameter types")
		}
	default:
		panic("ups: invalid handler parameter types")
	}

	if !reqType.Implements(messageType) {
		panic("ups: invalid handler parameter type")
	}

	if paramType != nil && !reflect.TypeOf(parameter).AssignableTo(paramType) {
		panic("ups: param does not match param parameter type")
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
	parameter         reflect.Value
	requestObjectPool sync.Pool
}

func (ups *upsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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
		case paramHandlerType:
			args = []reflect.Value{ups.parameter, arg}
		case contextParamHandlerType:
			args = []reflect.Value{reflect.ValueOf(ctx), ups.parameter, arg}
		case requestParamHandlerType:
			args = []reflect.Value{reflect.ValueOf(r), ups.parameter, arg}
		}

		results := ups.handler.Call(args)
		if len(results) > 1 && !results[1].IsNil() {
			if err, ok := results[1].Interface().(StatusCoder); ok {
				statusCode = err.StatusCode()
			} else {
				statusCode = http.StatusInternalServerError
			}
			return
		}
		result := results[0].Interface().(proto.Message)
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
