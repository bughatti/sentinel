package mqtt

import (
	"fmt"
	"log/slog"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/bughatti/sentinel/internal/config"
)

// Client wraps a Paho MQTT client with auto-reconnect and a Last Will &
// Testament that publishes "offline" on disconnect.
type Client struct {
	paho    paho.Client
	topics  Topics
	cfg     config.MQTTConfig
}

// NewClient creates and connects a Paho MQTT client. If the broker is
// unreachable on startup this returns an error; the client will continue to
// reconnect automatically after that.
func NewClient(cfg config.MQTTConfig) (*Client, error) {
	topics := Topics{Prefix: cfg.TopicPrefix}

	opts := paho.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.Host, cfg.Port))
	opts.SetClientID(cfg.ClientID)
	if cfg.User != "" {
		opts.SetUsername(cfg.User)
		opts.SetPassword(cfg.Password)
	}
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(cfg.ReconnectWait)
	opts.SetCleanSession(false)
	opts.SetKeepAlive(30 * time.Second)

	// Last Will Testament — published by broker on unclean disconnect.
	opts.SetWill(topics.Available(), "offline", 1, true)

	opts.SetOnConnectHandler(func(c paho.Client) {
		slog.Info("mqtt connected", "broker", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
		// Publish online availability.
		c.Publish(topics.Available(), 1, true, "online")
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		slog.Warn("mqtt connection lost", "err", err)
	})
	opts.SetReconnectingHandler(func(_ paho.Client, _ *paho.ClientOptions) {
		slog.Info("mqtt reconnecting...")
	})

	c := paho.NewClient(opts)
	token := c.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect to %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	return &Client{
		paho:   c,
		topics: topics,
		cfg:    cfg,
	}, nil
}

// Disconnect cleanly disconnects from the broker after publishing offline.
func (c *Client) Disconnect() {
	c.paho.Publish(c.topics.Available(), 1, true, "offline").Wait()
	c.paho.Disconnect(250)
	slog.Info("mqtt disconnected")
}

// Publish publishes payload to topic with QoS 1.
func (c *Client) Publish(topic string, retained bool, payload any) {
	token := c.paho.Publish(topic, 1, retained, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		slog.Warn("mqtt publish error", "topic", topic, "err", err)
	}
}

// IsConnected returns true if the underlying Paho client is connected.
func (c *Client) IsConnected() bool {
	return c.paho.IsConnected()
}

// Topics returns the topic helper for this client.
func (c *Client) Topics() Topics {
	return c.topics
}
