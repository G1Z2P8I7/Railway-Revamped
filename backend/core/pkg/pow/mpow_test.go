package pow

import (
	"testing"
	"time"
)

func TestMemoryHardPoWLifecycle(t *testing.T) {
	// 4 bits difficulty = 1 leading zero hex char (fast for test execution)
	engine := NewMemoryHardPoWEngine(4)

	// 1. Server generates challenge under surge
	ch, token := engine.GenerateChallenge()
	if ch.MemoryKB != 8*1024 {
		t.Fatalf("expected 8192 KB memory requirement, got: %d", ch.MemoryKB)
	}

	// 2. Client solves challenge
	start := time.Now()
	sol := engine.SolveClient(ch)
	duration := time.Since(start)
	t.Logf("Client solved 8MB Argon2 challenge in %v (Answer=%d, Hash=%s)", duration, sol.Answer, sol.HashHex)

	// 3. Server verifies solution
	err := engine.VerifySolution(token, sol)
	if err != nil {
		t.Fatalf("verification should succeed, got: %v", err)
	}

	// 4. Test invalid solution (tampered answer)
	tamperedSol := sol
	tamperedSol.Answer += 9999
	err = engine.VerifySolution(token, tamperedSol)
	if err != ErrInvalidProof {
		t.Fatalf("expected ErrInvalidProof for tampered answer, got: %v", err)
	}

	// 5. Test invalid hash
	tamperedHash := sol
	tamperedHash.HashHex = "ffff" + sol.HashHex[4:]
	err = engine.VerifySolution(token, tamperedHash)
	if err != ErrInvalidProof {
		t.Fatalf("expected ErrInvalidProof for tampered hash, got: %v", err)
	}

	// 5. Test tampered token
	tamperedToken := token + "tampered"
	err = engine.VerifySolution(tamperedToken, sol)
	if err != ErrInvalidProof {
		t.Fatalf("expected ErrInvalidProof for tampered token, got: %v", err)
	}
}

func BenchmarkArgon2Verification(b *testing.B) {
	engine := NewMemoryHardPoWEngine(4)
	ch, token := engine.GenerateChallenge()
	sol := engine.SolveClient(ch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.VerifySolution(token, sol)
	}
}
