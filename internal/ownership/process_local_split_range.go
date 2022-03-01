package ownership

import (
	"time"

	. "github.com/barcostreams/barco/internal/types"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func (o *generator) processLocalSplitRange(m *localSplitRangeGenMessage) creationError {
	const reason = "split range"
	topology := m.topology
	newBrokerOrdinal := m.origin
	newBrokerIndex := topology.GetIndex(newBrokerOrdinal)
	myToken := topology.MyToken()
	myCurrentGen := o.discoverer.Generation(myToken)
	newToken := topology.GetToken(newBrokerIndex)

	if o.discoverer.Generation(newToken) != nil {
		// Already created
		return nil
	}
	if myCurrentGen == nil {
		return newCreationError("Could not split range as generation not found for T%d", topology.MyOrdinal())
	}
	if myCurrentGen.Leader != topology.MyOrdinal() {
		return newCreationError("Could not split range as I'm not the leader of my token T%d", topology.MyOrdinal())
	}

	nextBrokers := topology.NextBrokers(topology.LocalIndex, 3)
	for _, b := range nextBrokers {
		if !o.gossiper.IsHostUp(b.Ordinal) {
			return newCreationError("Could not split range as B%d is not UP", b.Ordinal)
		}
	}

	log.Info().Msgf("Processing token range split T%d-T%d", topology.MyOrdinal(), nextBrokers[1].Ordinal)

	version := o.lastKnownVersion(newToken, nextBrokers) + 1
	log.Debug().Msgf("Identified v%d for T%d (%d)", version, newBrokerOrdinal, newToken)

	myGen := Generation{
		Start:     myToken,
		End:       newToken,
		Version:   myCurrentGen.Version + 1,
		Timestamp: time.Now().UnixMicro(),
		Leader:    topology.MyOrdinal(),
		Followers: ordinals(nextBrokers[:2]),
		TxLeader:  topology.MyOrdinal(),
		Tx:        uuid.New(),
		Status:    StatusProposed,
		Parents: []GenParent{{
			Start:   myCurrentGen.Start,
			Version: myCurrentGen.Version,
		}},
	}

	// Generation for the second part of the range
	nextTokenGen := Generation{
		Start:     newToken,
		End:       myCurrentGen.End,
		Version:   version,
		Timestamp: time.Now().UnixMicro(),
		Leader:    newBrokerOrdinal,
		Followers: ordinals(nextBrokers[1:3]),
		TxLeader:  topology.MyOrdinal(),
		Tx:        myGen.Tx,
		Status:    StatusProposed,
		Parents:   myGen.Parents,
	}

	readResults := o.readStateFromFollowers(&myGen)
	if readResults[0].Error != nil && readResults[1].Error != nil {
		return newCreationError("Followers state could not be read")
	}
	if isInProgress(readResults[0].Proposed) || isInProgress(readResults[1].Proposed) {
		return newCreationError("In progress generation in remote broker")
	}
	nextTokenLeaderRead := o.gossiper.GetGenerations(newBrokerOrdinal, newToken)
	if nextTokenLeaderRead.Error != nil {
		return newCreationError("Next token leader generation state could not be read: %s", nextTokenLeaderRead.Error)
	}

	followerErrors := o.setStateToFollowers(&myGen, nil, readResults)
	if followerErrors[0] != nil && followerErrors[1] != nil {
		return newCreationError("Followers state could not be set to proposed")
	}

	if err := o.discoverer.SetGenerationProposed(&myGen, nil); err != nil {
		log.Err(err).Msg("Unexpected error when setting as proposed locally")
		return newNonRetryableError("Unexpected local error")
	}

	log.Info().Msgf(
		"Proposed myself as a leader for T%d-T%d [%d, %d] as part of range splitting",
		topology.MyOrdinal(), newBrokerOrdinal, myToken, newToken)

	// Read the state from of nextTokenGen followers
	readResults = o.readStateFromFollowers(&nextTokenGen)
	if readResults[0].Error != nil && readResults[1].Error != nil {
		return newCreationError("Followers state could not be read")
	}
	if isInProgress(readResults[0].Proposed) || isInProgress(readResults[1].Proposed) {
		return newCreationError("In progress generation in remote broker")
	}

	// Set state in leader
	if err := o.gossiper.SetGenerationAsProposed(newBrokerOrdinal, &nextTokenGen, getTx(nextTokenLeaderRead.Proposed)); err != nil {
		return newCreationError("Next token leader generation state could not be set: %s", err)
	}
	nextTokenFollowerErrors := o.setStateToFollowers(&nextTokenGen, nil, readResults)
	if nextTokenFollowerErrors[0] != nil && nextTokenFollowerErrors[1] != nil {
		return newCreationError("Followers state could not be set to proposed")
	}

	// isUp, err := o.gossiper.ReadBrokerIsUp(peerFollower, downBroker.Ordinal)
	// if err != nil {
	// 	return wrapCreationError(err)
	// }

	// if isUp {
	// 	return newCreationError("Broker B%d is still consider as UP by B%d", downBroker.Ordinal, peerFollower)
	// }

	// token := topology.GetToken(index)

	// gen := Generation{
	// 	Start:     token,
	// 	End:       topology.GetToken(index + 1),
	// 	Version:   m.previousGen.Version + 1,
	// 	Timestamp: time.Now().UnixMicro(),
	// 	Leader:    topology.MyOrdinal(),
	// 	Followers: []int{peerFollower, downBroker.Ordinal},
	// 	TxLeader:  topology.MyOrdinal(),
	// 	Tx:        uuid.New(),
	// 	Status:    StatusProposed,
	// 	Parents: []GenParent{{
	// 		Start:   token,
	// 		Version: m.previousGen.Version,
	// 	}},
	// }

	// log.Info().
	// 	Str("reason", reason).
	// 	Msgf("Proposing myself as leader of T%d (%d) in v%d", downBroker.Ordinal, token, gen.Version)

	// committed, proposed := o.discoverer.GenerationProposed(token)

	// if !reflect.DeepEqual(m.previousGen, committed) {
	// 	log.Info().Msgf("New committed generation found, aborting creation")
	// 	return nil
	// }

	// peerFollowerGenInfo := o.gossiper.GetGenerations(peerFollower, gen.Start)
	// if peerFollowerGenInfo.Error != nil {
	// 	return newCreationError(
	// 		"Generation info could not be read from follower: %s", peerFollowerGenInfo.Error.Error())
	// }

	// if err := o.discoverer.SetGenerationProposed(&gen, getTx(proposed)); err != nil {
	// 	return wrapCreationError(err)
	// }

	// if err := o.gossiper.SetGenerationAsProposed(peerFollower, &gen, getTx(peerFollowerGenInfo.Proposed)); err != nil {
	// 	return wrapCreationError(err)
	// }

	// log.Info().
	// 	Str("reason", reason).
	// 	Msgf("Accepting myself as leader of T%d (%d) in v%d", downBroker.Ordinal, token, gen.Version)
	// gen.Status = StatusAccepted

	// if err := o.gossiper.SetGenerationAsProposed(peerFollower, &gen, &gen.Tx); err != nil {
	// 	return wrapCreationError(err)
	// }

	// if err := o.discoverer.SetGenerationProposed(&gen, &gen.Tx); err != nil {
	// 	log.Err(err).Msg("Unexpected error when setting as accepted locally")
	// 	return newCreationError("Unexpected local error")
	// }

	// // Now we have a majority of replicas
	// log.Info().
	// 	Str("reason", reason).
	// 	Msgf("Setting transaction for T%d (%d) as committed", downBroker.Ordinal, token)

	// // We can now start receiving producer traffic for this token
	// if err := o.discoverer.SetAsCommitted(gen.Start, gen.Tx, topology.MyOrdinal()); err != nil {
	// 	log.Err(err).Msg("Set as committed locally failed (probably local db related)")
	// 	return newCreationError("Set as committed locally failed")
	// }
	// o.gossiper.SetAsCommitted(peerFollower, gen.Start, gen.Tx)

	return nil
}

// func (o *generator) splitPropose() {

// }

// type splitPropose

func (o *generator) lastKnownVersion(token Token, peers []BrokerInfo) GenVersion {
	version := GenVersion(0)
	// As I'm the only one
	if gen, err := o.discoverer.GetTokenHistory(token); err != nil {
		log.Panic().Err(err).Msgf("Error retrieving token history")
	} else if gen != nil {
		version = gen.Version
	}

	peerVersions := make([]chan GenVersion, 0)
	for _, b := range peers {
		broker := b
		c := make(chan GenVersion)
		peerVersions = append(peerVersions, c)
		go func() {
			gen, err := o.gossiper.ReadTokenHistory(broker.Ordinal, token)
			if err != nil {
				log.Err(err).Msgf("Error retrieving token history from B%d", broker.Ordinal)
			}

			if gen != nil {
				c <- gen.Version
			} else {
				c <- GenVersion(0)
			}
		}()
	}

	for i := 0; i < len(peers); i++ {
		v := <-peerVersions[i]
		if v > version {
			version = v
		}
	}

	return version
}

func ordinals(brokers []BrokerInfo) []int {
	result := make([]int, len(brokers))
	for i, b := range brokers {
		result[i] = b.Ordinal
	}
	return result
}
