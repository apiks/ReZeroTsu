package guilds

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SetRaffleMessage points a raffle at its button message. It changes only the message's channel and message IDs, so
// entrants, the latest winner and a legacy reaction message stay as they are, unlike SetRaffle, which replaces the
// whole record.
func (r *Repo) SetRaffleMessage(ctx context.Context, guildID, raffleName, channelID, messageID string) error {
	filter := bson.M{"_id": guildID, "raffles.name": raffleName}
	update := bson.M{"$set": bson.M{"raffles.$.channel_id": channelID, "raffles.$.message_id": messageID}}
	if _, err := r.coll.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("SetRaffleMessage: %w", err)
	}
	return nil
}
