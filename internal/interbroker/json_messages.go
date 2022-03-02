package interbroker

import (
	. "github.com/barcostreams/barco/internal/types"
	. "github.com/google/uuid"
)

// Represents the interbroker api json message for proposing and accepting a generation to another broker.
type GenerationProposeMessage struct {
	Generation *Generation `json:"generation"`
	ExpectedTx *UUID       `json:"tx,omitempty"`
}

// GenerationCommitMessage the interbroker api json message for
// committing a generation to another broker.
type GenerationCommitMessage struct {
	Tx     UUID `json:"tx"`
	Origin int  `json:"origin"` // The ordinal of the originator of the transaction
}

type ConsumerGroupInfoMessage struct {
	Groups []ConsumerGroup `json:"groups"`
	Origin int             `json:"origin"` // The ordinal of the sender
}
