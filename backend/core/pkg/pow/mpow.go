package pow

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidProof     = errors.New("invalid memory-hard proof-of-work solution")
	ErrExpiredChallenge = errors.New("proof-of-work challenge has expired")
	ErrChallengeReplayed = errors.New("proof-of-work challenge nonce already consumed")
)

// Default surge parameters: 8MB memory, 1 iteration, 1 thread
const (
	DefaultMemoryKB   = 8 * 1024 // 8 MB RAM per computation thread
	DefaultIterations = 1
	DefaultThreads    = 1
	DefaultKeyLen     = 16
	ChallengeTTLSecs  = 60 // 60 seconds validity
)

type Challenge struct {
	Nonce          string `json:"nonce"`
	DifficultyBits int    `json:"difficulty_bits"` // e.g. 8 to 16 bits
	MemoryKB       uint32 `json:"memory_kb"`
	Iterations     uint32 `json:"iterations"`
	Threads        uint8  `json:"threads"`
	IssuedAtUnix   int64  `json:"issued_at"`
}

type Solution struct {
	Nonce    string `json:"nonce"`
	Answer   uint64 `json:"answer"`
	HashHex  string `json:"hash_hex"`
}

type MemoryHardPoWEngine struct {
	memoryKB       uint32
	iterations     uint32
	threads        uint8
	difficultyBits int
	secretSalt     []byte
}

func NewMemoryHardPoWEngine(difficultyBits int) *MemoryHardPoWEngine {
	if difficultyBits <= 0 {
		difficultyBits = 8 // Default light difficulty for legitimate mobile phones (~150-200ms)
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)

	return &MemoryHardPoWEngine{
		memoryKB:       DefaultMemoryKB,
		iterations:     DefaultIterations,
		threads:        DefaultThreads,
		difficultyBits: difficultyBits,
		secretSalt:     salt,
	}
}

// GenerateChallenge creates a memory-hard challenge token for clients under surge.
func (e *MemoryHardPoWEngine) GenerateChallenge() (*Challenge, string) {
	nonceBytes := make([]byte, 16)
	_, _ = rand.Read(nonceBytes)
	nonce := hex.EncodeToString(nonceBytes)

	now := time.Now().Unix()
	ch := &Challenge{
		Nonce:          nonce,
		DifficultyBits: e.difficultyBits,
		MemoryKB:       e.memoryKB,
		Iterations:     e.iterations,
		Threads:        e.threads,
		IssuedAtUnix:   now,
	}

	// Sign the challenge so it can be verified statelessly
	payload := fmt.Sprintf("%s:%d:%d:%d:%d:%d",
		ch.Nonce, ch.DifficultyBits, ch.MemoryKB, ch.Iterations, ch.Threads, ch.IssuedAtUnix)
	sig := e.signPayload(payload)
	token := base64.URLEncoding.EncodeToString([]byte(payload + "|" + sig))

	return ch, token
}

// SolveClient is a reference implementation of how mobile/web clients solve the challenge.
func (e *MemoryHardPoWEngine) SolveClient(ch *Challenge) Solution {
	var answer uint64
	targetPrefixZeros := ch.DifficultyBits / 4 // Hex chars that must be '0'

	for {
		candidate := fmt.Sprintf("%s:%d", ch.Nonce, answer)
		derived := argon2.IDKey([]byte(candidate), e.secretSalt, ch.Iterations, ch.MemoryKB, ch.Threads, DefaultKeyLen)
		hashHex := hex.EncodeToString(derived)

		if hasLeadingZeros(hashHex, targetPrefixZeros) {
			return Solution{
				Nonce:   ch.Nonce,
				Answer:  answer,
				HashHex: hashHex,
			}
		}
		answer++
	}
}

// VerifySolution validates that a submitted answer meets the memory-hard difficulty.
func (e *MemoryHardPoWEngine) VerifySolution(signedChallengeToken string, sol Solution) error {
	rawBytes, err := base64.URLEncoding.DecodeString(signedChallengeToken)
	if err != nil {
		return ErrInvalidProof
	}

	parts := strings.Split(string(rawBytes), "|")
	if len(parts) != 2 {
		return ErrInvalidProof
	}

	payload := parts[0]
	signature := parts[1]

	// Verify cryptographic signature of the challenge
	expectedSig := e.signPayload(payload)
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSig)) != 1 {
		return ErrInvalidProof
	}

	fields := strings.Split(payload, ":")
	if len(fields) != 6 {
		return ErrInvalidProof
	}

	nonce := fields[0]
	diffBits, _ := strconv.Atoi(fields[1])
	memKB, _ := strconv.ParseUint(fields[2], 10, 32)
	iter, _ := strconv.ParseUint(fields[3], 10, 32)
	threads, _ := strconv.ParseUint(fields[4], 10, 8)
	issuedAt, _ := strconv.ParseInt(fields[5], 10, 64)

	if nonce != sol.Nonce {
		return ErrInvalidProof
	}

	// Check expiration (60s TTL)
	if time.Now().Unix()-issuedAt > ChallengeTTLSecs {
		return ErrExpiredChallenge
	}

	// Execute memory-hard Argon2 verification (guarantees 8MB RAM allocation)
	candidate := fmt.Sprintf("%s:%d", sol.Nonce, sol.Answer)
	derived := argon2.IDKey([]byte(candidate), e.secretSalt, uint32(iter), uint32(memKB), uint8(threads), DefaultKeyLen)
	computedHex := hex.EncodeToString(derived)

	if sol.HashHex != "" && sol.HashHex != computedHex {
		return ErrInvalidProof
	}

	targetPrefixZeros := diffBits / 4
	if !hasLeadingZeros(computedHex, targetPrefixZeros) {
		return ErrInvalidProof
	}

	return nil
}

func hasLeadingZeros(hexStr string, count int) bool {
	if count <= 0 {
		return true
	}
	if len(hexStr) < count {
		return false
	}
	for i := 0; i < count; i++ {
		if hexStr[i] != '0' {
			return false
		}
	}
	return true
}

func (e *MemoryHardPoWEngine) signPayload(payload string) string {
	derived := argon2.IDKey([]byte(payload), e.secretSalt, 1, 64, 1, 16)
	return hex.EncodeToString(derived)
}
