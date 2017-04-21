package ups

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/qpliu/ups/testingups"
)

type testError int

func (err testError) Error() string {
	return strconv.Itoa(int(err))
}

func (err testError) StatusCode() int {
	return int(err)
}

func TestHello(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer func() {
		t.Log(logs.String())
	}()

	handler := UPS(func(req *testingups.HelloRequest) *testingups.HelloResponse {
		if req.Name == "panic" {
			panic(req.Name)
		}
		return &testingups.HelloResponse{Text: "Hello, " + req.Name + "!"}
	})

	handlerContext := UPS(func(ctx context.Context, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "Context, " + req.Name + "!"}
	})

	handlerRequest := UPS(func(httpReq *http.Request, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "Request, " + req.Name + "!"}
	})

	handlerParam := UPSWithParameter(func(parameter int, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "Parameter " + strconv.Itoa(parameter) + ", " + req.Name + "!"}
	}, 1)

	handlerContextParam := UPSWithParameter(func(ctx context.Context, parameter int, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "ContextParameter, " + req.Name + "!"}
	}, 1)

	handlerRequestParam := UPSWithParameter(func(httpReq *http.Request, parameter int, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "RequestParameter, " + req.Name + "!"}
	}, 1)

	handlerError := UPS(func(req *testingups.HelloRequest) (*testingups.HelloResponse, error) {
		switch req.Name {
		case "Error":
			return nil, errors.New("error")
		case "Teapot":
			return nil, testError(http.StatusTeapot)
		default:
			return &testingups.HelloResponse{Text: "Error, " + req.Name + "!"}, nil
		}
	})

	configNoJSON := DefaultConfig
	configNoJSON.JSONMarshaler = nil
	handlerNoJSON := UPSWithConfig(func(httpReq *http.Request, req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "No JSON, " + req.Name + "!"}
	}, configNoJSON)

	t.Run("json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString(`{"name":"World"}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/json" {
			t.Errorf("response Content-Type, expected: application/json, got: %s", respContentType)
		}
		respBody := resp.Body.String()
		respBodyExpected := `{"text":"Hello, World!"}`
		if respBody != respBodyExpected {
			t.Errorf("response body, expected: %s, got: %s", respBodyExpected, respBody)
		}
	})

	t.Run("protobuf", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			13, 'H', 'e', 'l', 'l', 'o', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("bad content-type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString("bad request"))
		req.Header.Set("Content-Type", "text/plain")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnsupportedMediaType {
			t.Errorf("response code: expected: %d, got: %d", http.StatusUnsupportedMediaType, resp.Code)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString("bad request"))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusInternalServerError {
			t.Errorf("response code: expected: %d, got: %d", http.StatusInternalServerError, resp.Code)
		}
	})

	t.Run("bad protobuf", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString("bad request"))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusInternalServerError {
			t.Errorf("response code: expected: %d, got: %d", http.StatusInternalServerError, resp.Code)
		}
	})

	t.Run("panic", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'p', 'a', 'n', 'i', 'c',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusInternalServerError {
			t.Errorf("response code: expected: %d, got: %d", http.StatusInternalServerError, resp.Code)
		}
	})

	t.Run("protobuf-context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerContext.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			15, 'C', 'o', 'n', 't', 'e', 'x', 't', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("protobuf-request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerRequest.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			15, 'R', 'e', 'q', 'u', 'e', 's', 't', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/hello", &bytes.Buffer{})
		resp := httptest.NewRecorder()
		handlerRequest.ServeHTTP(resp, req)
		if resp.Code != http.StatusMethodNotAllowed {
			t.Errorf("response code: expected: %d, got: %d", http.StatusMethodNotAllowed, resp.Code)
		}
	})

	t.Run("protobuf,no json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerNoJSON.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			15, 'N', 'o', ' ', 'J', 'S', 'O', 'N', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("json,no json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString(`{"name":"World"}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		handlerNoJSON.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnsupportedMediaType {
			t.Errorf("response code: expected: %d, got: %d", http.StatusUnsupportedMediaType, resp.Code)
		}
	})

	t.Run("protobuf-param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerParam.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			19, 'P', 'a', 'r', 'a', 'm', 'e', 't', 'e', 'r', ' ',
			'1', ',', ' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("protobuf-context-param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerContextParam.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			24, 'C', 'o', 'n', 't', 'e', 'x', 't',
			'P', 'a', 'r', 'a', 'm', 'e', 't', 'e', 'r', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("protobuf-request-param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte{
			0x0a, // Field 1, wire type 2 (string)
			5, 'W', 'o', 'r', 'l', 'd',
		}))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp := httptest.NewRecorder()
		handlerRequestParam.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respContentType := resp.HeaderMap.Get("Content-Type")
		if respContentType != "application/octet-stream" {
			t.Errorf("response Content-Type: expected: application/octet-stream, got: %s", respContentType)
		}
		respBody := resp.Body.Bytes()
		respBodyExpected := []byte{
			0x0a, // Field 1, wire type 2 (string)
			24, 'R', 'e', 'q', 'u', 'e', 's', 't',
			'P', 'a', 'r', 'a', 'm', 'e', 't', 'e', 'r', ',',
			' ', 'W', 'o', 'r', 'l', 'd', '!',
		}
		if bytes.Compare(respBody, respBodyExpected) != 0 {
			t.Errorf("response body, expected: %x, got: %x", respBodyExpected, respBody)
		}
	})

	t.Run("error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString(`{"name":"Success"}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		handlerError.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Errorf("response code: expected: %d, got: %d", http.StatusOK, resp.Code)
		}
		respBody := resp.Body.String()
		respBodyExpected := `{"text":"Error, Success!"}`
		if respBody != respBodyExpected {
			t.Errorf("response body, expected: %s, got: %s", respBodyExpected, respBody)
		}

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString(`{"name":"Teapot"}`))
		req.Header.Set("Content-Type", "application/json")
		resp = httptest.NewRecorder()
		handlerError.ServeHTTP(resp, req)
		if resp.Code != http.StatusTeapot {
			t.Errorf("response code: expected: %d, got: %d", http.StatusTeapot, resp.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBufferString(`{"name":"Error"}`))
		req.Header.Set("Content-Type", "application/json")
		resp = httptest.NewRecorder()
		handlerError.ServeHTTP(resp, req)
		if resp.Code != http.StatusInternalServerError {
			t.Errorf("response code: expected: %d, got: %d", http.StatusInternalServerError, resp.Code)
		}
	})
}

func ExampleUPS() {
	http.Handle("/hello", UPS(func(req *testingups.HelloRequest) *testingups.HelloResponse {
		return &testingups.HelloResponse{Text: "Hello, " + req.Name + "!"}
	}))
}
