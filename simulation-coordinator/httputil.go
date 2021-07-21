package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"

	"nhooyr.io/websocket"
)

func sendReq(ctx context.Context, method, url string, header http.Header, data io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, data)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if header != nil {
		req.Header = header
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return resp, fmt.Errorf("failed to send request: %w", err)
	}
	return resp, nil
}

func sendJSON(ctx context.Context, method, url string, header http.Header, out, in interface{}) (err error) {
	var resp *http.Response
	if header == nil {
		header = http.Header{}
	}
	if in == nil {
		resp, err = sendReq(ctx, method, url, header, nil)
	} else {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return fmt.Errorf("failed to encode payload %w:", err)
		}
		header.Set("Content-Type", "application/json")
		resp, err = sendReq(ctx, method, url, header, &buf)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	defer io.Copy(ioutil.Discard, resp.Body)
	if resp.StatusCode != 200 {
		defer func() {
			err = fmt.Errorf("request failed: %s: %w", resp.Status, err)
		}()
		var buf bytes.Buffer
		if _, err = io.Copy(&buf, resp.Body); err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		respErr := apiError{
			Message: resp.Status,
			BodyStr: buf.String(),
		}
		json.NewDecoder(&buf).Decode(&respErr.Body)
		return &respErr
	}
	if out == nil {
		return nil
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func getJSON(ctx context.Context, url string, out interface{}) (err error) {
	return sendJSON(ctx, "GET", url, nil, out, nil)
}

func postJSON(ctx context.Context, url string, header http.Header, out, in interface{}) (err error) {
	return sendJSON(ctx, "POST", url, header, out, in)
}

func deleteJSON(ctx context.Context, url string, out, in interface{}) (err error) {
	return sendJSON(ctx, "DELETE", url, nil, out, in)
}

// obj is a shorthand for JSON objects.
type obj = map[string]interface{}

type apiError struct {
	Message string
	BodyStr string
	Body    interface{}
}

func (e *apiError) Error() string {
	return e.BodyStr
}

type websocketConn struct {
	*websocket.Conn
}

func connectWebSocket(ctx context.Context, url string) (*websocketConn, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	return &websocketConn{c}, err
}

func acceptWebsocket(w http.ResponseWriter, r *http.Request) (*websocketConn, error) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	return &websocketConn{conn}, err
}

func (c *websocketConn) ReadJSON(ctx context.Context, obj interface{}) error {
	_, r, err := c.Reader(ctx)
	if err != nil {
		return err
	}
	defer io.Copy(ioutil.Discard, r)
	return json.NewDecoder(r).Decode(obj)
}

func (c *websocketConn) WriteJSON(ctx context.Context, obj interface{}) error {
	w, err := c.Writer(ctx, websocket.MessageText)
	if err != nil {
		return err
	}
	defer w.Close()
	return json.NewEncoder(w).Encode(obj)
}

func (c *websocketConn) WriteError(ctx context.Context, message string, err error) error {
	text := message
	if err != nil {
		text = fmt.Sprintf("%s: %v", message, err)
	}
	log.Println(text)
	return c.WriteJSON(ctx, obj{"error": text})
}
