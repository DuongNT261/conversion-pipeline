// Package queue talks to Pub/Sub: the relay publishes row ids, the worker
// receives them (docs/01 section 6.2).
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// RowMessage is the message body. It only carries the row id (claim check):
// the worker reads the latest state of the row from MySQL itself.
type RowMessage struct {
	RowID int64 `json:"row_id"`
}

// NewClient connects to the Pub/Sub emulator at host. The library would read
// PUBSUB_EMULATOR_HOST by itself, but then the docs/01 default (pubsub:8085)
// would not apply and an unset variable would silently target real Google Cloud.
// These three options are what the library does for the emulator.
func NewClient(ctx context.Context, host, projectID string) (*pubsub.Client, error) {
	client, err := pubsub.NewClient(ctx, projectID,
		option.WithEndpoint(host),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		return nil, fmt.Errorf("pubsub client host=%s: %w", host, err)
	}
	return client, nil
}

// EnsureTopic creates the topic and its subscription when they are missing
// (REL-6); the emulator starts empty and forgets both on every restart.
// It creates first and treats AlreadyExists as success, so two relays starting
// at the same time cannot fail each other.
func EnsureTopic(ctx context.Context, client *pubsub.Client, topicID, subscriptionID string) (*pubsub.Topic, error) {
	topic, err := client.CreateTopic(ctx, topicID)
	if status.Code(err) == codes.AlreadyExists {
		topic = client.Topic(topicID)
	} else if err != nil {
		return nil, fmt.Errorf("create topic %s: %w", topicID, err)
	}

	_, err = client.CreateSubscription(ctx, subscriptionID, pubsub.SubscriptionConfig{Topic: topic})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return nil, fmt.Errorf("create subscription %s: %w", subscriptionID, err)
	}
	return topic, nil
}

// PublishRows publishes one message per row and waits until Pub/Sub has
// accepted every one of them (REL-2). Publish only queues a message and returns
// at once; Get blocks until the server answers for that message.
func PublishRows(ctx context.Context, topic *pubsub.Topic, rows []store.PendingRow) error {
	results := make([]*pubsub.PublishResult, 0, len(rows))
	for _, r := range rows {
		data, _ := json.Marshal(RowMessage{RowID: r.ID}) // a struct with one int64 cannot fail to encode
		msg := &pubsub.Message{
			Data:       data,
			Attributes: map[string]string{"job_id": strconv.FormatInt(r.JobID, 10)},
		}
		results = append(results, topic.Publish(ctx, msg))
	}

	for _, res := range results {
		if _, err := res.Get(ctx); err != nil {
			return fmt.Errorf("publish row: %w", err)
		}
	}
	return nil
}
