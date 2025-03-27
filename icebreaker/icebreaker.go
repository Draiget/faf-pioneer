package icebreaker

import (
	"context"
	"encoding/json"
	"faf-pioneer/applog"
	"fmt"
	"go.uber.org/zap"
	"resty.dev/v3"
)

type Client struct {
	apiRoot      string
	gameId       uint64
	accessToken  string
	sessionToken string
	httpClient   *resty.Client
	ctx          context.Context
}

func NewClient(ctx context.Context, apiRoot string, gameId uint64, accessToken string) *Client {
	return &Client{
		apiRoot:      apiRoot,
		gameId:       gameId,
		accessToken:  accessToken,
		sessionToken: "",
		httpClient:   resty.New(),
		ctx:          ctx,
	}
}

func (c *Client) WriteLogEntryToRemote(_ []*applog.LogEntry) error {
	// TODO: Endpoint to accept log entries on icebreaker side:
	// 		 see: https://github.com/FAForever/faf-pioneer/issues/10
	//		 see: https://github.com/FAForever/faf-icebreaker/issues/68

	// TODO: Remote endpoint should check `c.accessToken` to accept logs and
	//		 have rate-limiting acceptance for each user, other way is to aggregate logs
	//		 and sent them in chunks.

	// Here we should use `OnlyLocal()` otherwise it will cause stack overflow:
	// calling debug which is calling remoteWrite, which is again calling debug and remoteWrite.
	//
	// applog.OnlyLocal().Debug("Sending remote log entry!")`
	return nil
}

func (c *Client) withSessionToken() error {
	if c.sessionToken != "" {
		return nil
	}

	url := fmt.Sprintf("%s/session/token", c.apiRoot)

	requestData := SessionTokenRequest{
		GameId: c.gameId,
	}

	var result SessionTokenResponse

	// Make the POST request with JSON payload and Authorization header
	resp, err := c.httpClient.R().
		SetContext(c.ctx).
		SetAuthToken(c.accessToken).
		SetContentType("application/json").
		SetBody(requestData).
		SetResult(&result).
		Post(url)

	if err != nil {
		return fmt.Errorf("fetching session token failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("fetching session token failed: %v", resp.Status())
	}

	c.sessionToken = result.Jwt

	return nil
}

func (c *Client) GetGameSession() (*SessionGameResponse, error) {
	applog.Info("Getting game session id from ICE-Breaker API")
	err := c.withSessionToken()

	if err != nil {
		return nil, err
	}

	// Construct the URL with the gameId
	url := fmt.Sprintf("%s/session/game/%d", c.apiRoot, c.gameId)

	var result SessionGameResponse

	// Create a new HTTP request
	resp, err := c.httpClient.R().
		SetContext(c.ctx).
		SetAuthToken(c.accessToken).
		SetContentType("application/json").
		SetResult(&result).
		Get(url)

	if err != nil {
		return nil, fmt.Errorf("fetching game session failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("fetching game session failed: %v", resp.Status())
	}

	return &result, nil
}

func (c *Client) SendEvent(msg EventMessage) error {
	err := c.withSessionToken()

	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/session/game/%d/events", c.apiRoot, c.gameId)

	m, _ := json.Marshal(msg)
	applog.Debug("Sending event to ICE-Breaker API", zap.String("body", string(m)))

	// Make the POST request with JSON payload and Authorization header
	resp, err := c.httpClient.R().
		SetContext(c.ctx).
		SetAuthToken(c.sessionToken).
		SetContentType("application/json").
		SetBody(msg).
		Post(url)

	if err != nil {
		return fmt.Errorf("posting session event failed: %v", err)
	}

	if resp.StatusCode() != 204 {
		return fmt.Errorf("posting session event failed: %v", resp.Status())
	}

	return nil
}

func (c *Client) Listen(channel chan EventMessage) error {
	err := c.withSessionToken()

	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/session/game/%d/events", c.apiRoot, c.gameId)

	eventSource := resty.NewEventSource().
		SetURL(url).
		SetHeader("Authorization", fmt.Sprintf("Bearer %s", c.sessionToken)).
		OnMessage(func(message any) {
			restyEvent, ok := message.(*resty.Event)
			if !ok {
				applog.Error(
					"Invalid event format received from ICE-Breaker event",
					zap.Any("message", message),
				)
				return
			}

			event, parseErr := ParseEventMessage(restyEvent.Data)
			if parseErr != nil {
				applog.Error(
					"Failed parsing event received from ICE-Breaker event",
					zap.Any("message", message),
					zap.Error(parseErr),
				)
				return
			}

			switch e := event.(type) {
			case *ConnectedMessage:
				applog.Debug("Handing ICE-Breaker API event",
					zap.Any("event", e),
					zap.String("eventType", e.EventType),
				)
			case *CandidatesMessage:
				applog.Debug("Handing ICE-Breaker API event",
					zap.Any("event", e),
					zap.String("eventType", e.EventType),
				)
			default:
				applog.Debug("Handing unknown ICE-Breaker API event",
					zap.Any("event", e),
				)
			}

			channel <- event
		}, nil)

	applog.Info("Listening for ICE-Breaker API (server-side) events", zap.String("url", url))

	go func() {
		<-c.ctx.Done()
		eventSource.Close()
	}()

	err = eventSource.Get()

	if err != nil {
		return fmt.Errorf("could not attach to message event endpoint: %s", err)
	}

	return nil
}
