package discovery

import (
	"fmt"
	"sync/atomic"

	. "github.com/barcostreams/barco/internal/types"
	"github.com/barcostreams/barco/internal/utils"
	. "github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type GenerationState interface {
	// Generation gets a snapshot of the active generation by token.
	// This is part of the hot path.
	Generation(token Token) *Generation

	// GenerationInfo gets the information of a past committed generation.
	// Returns nil when not found.
	GenerationInfo(token Token, version GenVersion) *Generation

	// For an old generation, get's the following generation (or two in the case of split).
	// Returns nil when not found.
	NextGeneration(token Token, version GenVersion) []Generation

	// GenerationProposed reads a snapshot of the current committed and proposed generations
	GenerationProposed(token Token) (committed *Generation, proposed *Generation)

	// SetProposed compares and sets the proposed/accepted generation.
	//
	// Checks that the previous tx matches or is nil.
	// Also checks that provided gen.version is equal to committed plus one.
	SetGenerationProposed(gen *Generation, expectedTx *UUID) error

	// SetAsCommitted sets the transaction as committed, storing the history and
	// setting the proposed generation as committed
	//
	// Returns an error when transaction does not match
	SetAsCommitted(token Token, tx UUID, origin int) error

	// Determines whether there's active range containing (but not starting) the token
	IsTokenInRange(token Token) bool

	// Determines whether there's history matching the token
	HasTokenHistory(token Token) (bool, error)

	// Gets the last known committed token from the local persistence
	GetTokenHistory(token Token) (*Generation, error)
}

// Loads all generations from local storage
func (d *discoverer) loadGenerations() error {
	defer d.genMutex.Unlock()
	d.genMutex.Lock()

	if existing := d.generations.Load(); existing != nil {
		if existingMap := existing.(genMap); len(existingMap) > 0 {
			return fmt.Errorf("Generation map is not empty")
		}
	}

	genList, err := d.localDb.LatestGenerations()
	if err != nil {
		return err
	}

	newMap := make(genMap)
	for _, gen := range genList {
		newMap[gen.Start] = gen
	}
	d.generations.Store(newMap)
	return nil
}

func (d *discoverer) Generation(token Token) *Generation {
	existingMap := d.generations.Load().(genMap)

	if v, ok := existingMap[token]; ok {
		return &v
	}
	return nil
}

func (d *discoverer) GenerationInfo(token Token, version GenVersion) *Generation {
	gen, err := d.localDb.GenerationInfo(token, version)
	utils.PanicIfErr(err, "Generation info failed to be retrieved")
	return gen
}

func (d *discoverer) NextGeneration(token Token, version GenVersion) []Generation {
	current := d.GenerationInfo(token, version)
	if current == nil {
		return nil
	}

	nextGens, err := d.localDb.GenerationsByParent(current)
	utils.PanicIfErr(err, "Generations by parent failed to be retrieved")

	// TODO: Handle the case where this token was joined with another one and v+1 does not exist

	return nextGens
}

func (d *discoverer) GenerationProposed(token Token) (committed *Generation, proposed *Generation) {
	defer d.genMutex.Unlock()
	// All access to gen proposed must be lock protected
	d.genMutex.Lock()

	proposed = nil
	if v, ok := d.genProposed[token]; ok {
		proposed = &v
	}

	committed = d.Generation(token)
	return
}

func (d *discoverer) IsTokenInRange(token Token) bool {
	generationMap := d.generations.Load().(genMap)

	// O(n) is not that bad given the number of
	// active generations managed by broker
	for _, gen := range generationMap {
		// containing the token but not the start token
		// Note: end token is never contained
		if token > gen.Start && (token < gen.End || gen.End == StartToken) {
			return true
		}
	}
	return false
}

func (d *discoverer) HasTokenHistory(token Token) (bool, error) {
	result, err := d.localDb.GetGenerationsByToken(token)
	return len(result) > 0, err
}

func (d *discoverer) GetTokenHistory(token Token) (*Generation, error) {
	result, err := d.localDb.GetGenerationsByToken(token)
	if len(result) == 0 {
		return nil, err
	}
	return &result[0], nil
}

func (d *discoverer) SetGenerationProposed(gen *Generation, expectedTx *UUID) error {
	defer d.genMutex.Unlock()
	d.genMutex.Lock()

	var currentTx *UUID = nil
	if existingGen, ok := d.genProposed[gen.Start]; ok {
		currentTx = &existingGen.Tx
	}

	if currentTx != nil && expectedTx == nil {
		return fmt.Errorf("Existing transaction is not nil")
	}

	if currentTx == nil && expectedTx != nil {
		return fmt.Errorf("Existing transaction is nil and expected not to be")
	}

	if expectedTx != nil && currentTx != nil && *currentTx != *expectedTx {
		return fmt.Errorf("Existing proposed does not match: %s (expected %s)", currentTx, expectedTx)
	}

	// Get existing committed
	committed := d.Generation(gen.Start)

	if committed != nil && gen.Version <= committed.Version {
		return fmt.Errorf(
			"Proposed version is not the next version of committed: committed = %d, proposed = %d",
			committed.Version,
			gen.Version)
	}

	log.Info().Msgf(
		"%s version %d with leader %d for range [%d, %d]",
		gen.Status, gen.Version, gen.Leader, gen.Start, gen.End)

	// Replace entire proposed value
	d.genProposed[gen.Start] = *gen

	return nil
}

func (d *discoverer) SetAsCommitted(token Token, tx UUID, origin int) error {
	defer d.genMutex.Unlock()
	d.genMutex.Lock()

	// Set the transaction and the generation value as committed
	gen, ok := d.genProposed[token]

	if !ok {
		return fmt.Errorf("No proposed value found")
	}

	if gen.Tx != tx {
		return fmt.Errorf("Transaction does not match")
	}

	log.Info().Msgf(
		"Setting committed version %d with leader %d for range [%d, %d]", gen.Version, gen.Leader, gen.Start, gen.End)
	gen.Status = StatusCommitted

	// Store the history and the tx table first
	// that way db failures don't affect local state
	if err := d.localDb.CommitGeneration(&gen); err != nil {
		return err
	}

	copyAndStore(&d.generations, gen)

	// Remove from proposed
	delete(d.genProposed, token)
	return nil
}

func copyAndStore(generations *atomic.Value, gen Generation) {
	existingMap := generations.Load().(genMap)

	// Shallow copy existing
	newMap := make(genMap, len(existingMap))
	for k, v := range existingMap {
		newMap[k] = v
	}

	if !gen.ToDelete {
		newMap[gen.Start] = gen
	} else {
		delete(newMap, gen.Start)
	}
	generations.Store(newMap)
}
