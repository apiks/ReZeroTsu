package database

import (
	"context"
	"fmt"
	"time"

	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/database/reminders"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const dbName = "zerotsu"

// Collection names
const (
	colAnimeSubs = "anime_subs"
	colGuilds    = "guilds"
	colReminders = "reminders"
)

// Client wraps MongoDB and exposes collection repos.
type Client struct {
	client *mongo.Client
	db     *mongo.Database
}

// defaultMaxPoolSize is the MongoDB connection pool size; default driver is 100.
const defaultMaxPoolSize = 200

func NewClient(ctx context.Context, uri string, timeout time.Duration) (*Client, error) {
	opts := options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(timeout).
		SetMaxPoolSize(defaultMaxPoolSize)
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &Client{
		client: client,
		db:     client.Database(dbName),
	}, nil
}

// Ping verifies connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx, nil)
}

func (c *Client) Close(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}

func (c *Client) Guilds() *guilds.Repo {
	return guilds.NewRepo(c.db.Collection(colGuilds))
}

func (c *Client) AnimeSubs() *anime_subs.Repo {
	return anime_subs.NewRepo(c.db.Collection(colAnimeSubs))
}

func (c *Client) Reminders() *reminders.Repo {
	return reminders.NewRepo(c.db.Collection(colReminders))
}

// EnsureIndexes creates indexes on all collections. Call once after connect.
func (c *Client) EnsureIndexes(ctx context.Context) error {
	if err := c.Guilds().EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("EnsureIndexes guilds: %w", err)
	}
	if err := c.AnimeSubs().EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("EnsureIndexes anime_subs: %w", err)
	}
	if err := c.Reminders().EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("EnsureIndexes reminders: %w", err)
	}
	return nil
}
