// Package main implements a simple bank account.
package main

import "fmt"

// Account holds a balance for a named account holder.
type Account struct {
	Name    string
	Balance float64
}

// Withdraw subtracts amount from the account balance.
// Returns an error if the amount exceeds the balance.
func (a *Account) Withdraw(amount float64) error {
	if amount > a.Balance {
		return fmt.Errorf("insufficient funds: balance %.2f, requested %.2f", a.Balance, amount)
	}
	a.Balance -= amount
	return nil
}

// String returns a human-readable account summary.
func (a *Account) String() string {
	return fmt.Sprintf("%s: $%.2f", a.Name, a.Balance)
}

func main() {
	acct := &Account{Name: "Alice", Balance: 100.00}
	fmt.Println(acct)

	if err := acct.Withdraw(30.00); err != nil {
		fmt.Println("Error:", err)
	}
	fmt.Println(acct)
}
