package main

import "testing"

func TestWithdraw(t *testing.T) {
	acct := &Account{Name: "Test", Balance: 100.00}

	if err := acct.Withdraw(30.00); err != nil {
		t.Fatalf("Withdraw(30) error: %v", err)
	}
	if acct.Balance != 70.00 {
		t.Errorf("balance = %.2f, want 70.00", acct.Balance)
	}
}

func TestWithdraw_InsufficientFunds(t *testing.T) {
	acct := &Account{Name: "Test", Balance: 50.00}

	if err := acct.Withdraw(100.00); err == nil {
		t.Error("Withdraw(100) should fail with insufficient funds")
	}
}

func TestString(t *testing.T) {
	acct := &Account{Name: "Alice", Balance: 42.50}
	got := acct.String()
	want := "Alice: $42.50"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
