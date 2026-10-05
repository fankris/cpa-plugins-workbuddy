package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// Uses CPA 8.0.15's host-owned operation bridge. No second HTTP transport and
// no automatic retry: cancellation may race an already accepted upstream action.
type hostHTTPOperation struct {
	id, callbackID          string
	cancelOnce, finishOnce  sync.Once
	stopRequest, stopPlugin func() bool
}

func openHostHTTPOperation(req *http.Request) (*hostHTTPOperation, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if !pluginWorkerStart() {
		return nil, errPluginQuiescing
	}
	success := false
	defer func() {
		if !success {
			pluginWorkerDone()
		}
	}()
	callbackID := hostCallbackIDFromRequest(req)
	raw, err := hostCall(pluginabi.MethodHostHTTPOperationOpen, mustJSON(map[string]string{"host_callback_id": callbackID}))
	if err != nil {
		return nil, fmt.Errorf("host HTTP operation open: %w", err)
	}
	result, err := hostBridgeUnwrap(raw, pluginabi.MethodHostHTTPOperationOpen)
	if err != nil {
		return nil, err
	}
	var response struct {
		OperationID string `json:"operation_id"`
	}
	if err = json.Unmarshal(result, &response); err != nil || response.OperationID == "" {
		return nil, fmt.Errorf("host HTTP operation open returned no operation_id")
	}
	op := &hostHTTPOperation{id: response.OperationID, callbackID: callbackID}
	op.stopRequest = context.AfterFunc(req.Context(), op.cancel)
	op.stopPlugin = context.AfterFunc(pluginContext(), op.cancel)
	success = true
	return op, nil
}

func (op *hostHTTPOperation) cancel() {
	if op == nil {
		return
	}
	op.cancelOnce.Do(func() {
		_, _ = hostCall(pluginabi.MethodHostHTTPCancel, mustJSON(map[string]string{"host_callback_id": op.callbackID, "operation_id": op.id}))
	})
}

func (op *hostHTTPOperation) finish() {
	if op == nil {
		return
	}
	op.finishOnce.Do(func() {
		op.stopRequest()
		op.stopPlugin()
		// Joins any cancellation callback already running before releasing the worker.
		// For a completed operation host cancellation is an idempotent no-op.
		op.cancel()
		pluginWorkerDone()
	})
}
