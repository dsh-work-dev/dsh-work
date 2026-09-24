package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/local/dsh-work/internal/workeripc"
)

const maxPluginManagerResponse = 2 << 20

type pluginManagerRemote struct {
	EntryID        string `json:"entryId"`
	ModuleName     string `json:"moduleName"`
	Enabled        bool   `json:"enabled"`
	PatchID        string `json:"patchId"`
	ReadOnlyReason string `json:"readOnlyReason"`
}

type pluginManagerBundle struct {
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Installed      bool              `json:"installed"`
	ReadOnlyReason string            `json:"readOnlyReason"`
	Error          *pluginManagerErr `json:"error"`
}

type pluginManagerChange struct {
	Changed     bool              `json:"changed"`
	Application string            `json:"application"`
	Stage       string            `json:"stage"`
	Target      string            `json:"target"`
	Enabled     *bool             `json:"enabled"`
	Error       *pluginManagerErr `json:"error"`
	Warnings    []string          `json:"warnings"`
}

type pluginManagerErr struct {
	Code       string `json:"code"`
	Diagnostic string `json:"diagnostic"`
}

type pluginRemoteRequest struct {
	Type    string `json:"type"`
	RPCID   string `json:"rpcId"`
	Method  string `json:"method"`
	Payload any    `json:"payload"`
}

type pluginRemoteResponse struct {
	Type   string `json:"type"`
	RPCID  string `json:"rpcId"`
	Result struct {
		OK    bool             `json:"ok"`
		Value json.RawMessage  `json:"value"`
		Error *pluginRemoteErr `json:"error"`
	} `json:"result"`
}

type pluginRemoteErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *Host) callPluginManager(ctx context.Context, method string, args any, value any) error {
	switch method {
	case "setBundleEnabled", "setPluginEnabled", "listPlugins", "listBundles":
	default:
		return errors.New("unsupported DSH PluginManager method")
	}
	provider, ok := h.deps.Channel.(interface{ Current() workerchannel.Session })
	if !ok {
		return errors.New("the current DSH Worker does not expose its authenticated Connection")
	}
	session := provider.Current()
	if session == nil {
		return errors.New("the current DSH Worker has no authenticated Connection")
	}
	status := h.Status()
	run := h.activeRun()
	if status.State != lifecycle.StateReady || run == nil || status.GenerationID == "" || run.generation != status.GenerationID || session.Generation() != run.generation {
		return errors.New("the authenticated DSH Connection does not belong to the current Ready Worker")
	}
	endpoint := "pluginManager/" + method
	requestID := lifecycle.NewCorrelationID()
	body, err := json.Marshal(pluginRemoteRequest{
		Type: "client-request", RPCID: requestID, Method: endpoint,
		Payload: map[string]any{"args": args},
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, workeripc.Origin+"/api/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", workeripc.Origin)
	response, err := session.Client().Do(request)
	if err != nil {
		return fmt.Errorf("DSH PluginManager %s request failed: %w", method, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxPluginManagerResponse+1))
	if err != nil {
		return fmt.Errorf("DSH PluginManager %s response could not be read: %w", method, err)
	}
	if len(responseBody) > maxPluginManagerResponse {
		return fmt.Errorf("DSH PluginManager %s response exceeded the size limit", method)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("DSH PluginManager %s returned HTTP %d", method, response.StatusCode)
	}
	var envelope pluginRemoteResponse
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("DSH PluginManager %s returned an invalid Remote response: %w", method, err)
	}
	if envelope.Type != "server-response" || envelope.RPCID != requestID {
		return fmt.Errorf("DSH PluginManager %s returned a mismatched Remote response", method)
	}
	if !envelope.Result.OK {
		if envelope.Result.Error == nil {
			return fmt.Errorf("DSH PluginManager %s failed", method)
		}
		return fmt.Errorf("DSH PluginManager %s failed (%s): %s", method, envelope.Result.Error.Code, envelope.Result.Error.Message)
	}
	if len(envelope.Result.Value) == 0 || string(envelope.Result.Value) == "null" {
		return fmt.Errorf("DSH PluginManager %s returned no value", method)
	}
	if err := json.Unmarshal(envelope.Result.Value, value); err != nil {
		return fmt.Errorf("DSH PluginManager %s returned an invalid value: %w", method, err)
	}
	return nil
}

func pluginManagerChangeFailure(change pluginManagerChange) error {
	if change.Application != "failed" && change.Application != "cancelled" {
		return nil
	}
	if change.Error != nil {
		message := "DSH could not apply the plugin change"
		if strings.TrimSpace(change.Error.Diagnostic) != "" {
			message += ": " + strings.TrimSpace(change.Error.Diagnostic)
		} else if change.Error.Code != "" {
			message += " (" + change.Error.Code + ")"
		}
		return errors.New(message)
	}
	return fmt.Errorf("DSH could not apply the plugin change (%s)", change.Application)
}
