// Package name generates random agent names.
package name

import (
	"math/rand"
	"time"
)

var adjectives = []string{
	"bold", "brave", "bright", "calm", "clever",
	"cool", "eager", "fair", "fast", "fierce",
	"fluffy", "gentle", "happy", "jolly", "keen",
	"kind", "lively", "lucky", "merry", "mighty",
	"noble", "proud", "quick", "quiet", "sharp",
	"silly", "sleek", "smart", "snappy", "speedy",
	"steady", "swift", "tender", "tough", "vivid",
	"warm", "wild", "wise", "witty", "zany",
	"zen", "zesty", "agile", "alert", "bold",
	"cosmic", "daring", "epic", "focal", "grand",
}

var animals = []string{
	"badger", "bear", "beaver", "bison", "cat",
	"cheetah", "chicken", "coyote", "crane", "crow",
	"deer", "dog", "dolphin", "dove", "dragon",
	"eagle", "falcon", "ferret", "finch", "fox",
	"frog", "gopher", "hawk", "heron", "horse",
	"jaguar", "koala", "lemur", "lion", "lynx",
	"meerkat", "moose", "narwhal", "octopus", "otter",
	"owl", "panda", "parrot", "penguin", "pigeon",
	"puma", "quail", "rabbit", "raven", "salmon",
	"seal", "shark", "snake", "sparrow", "spider",
	"squid", "swan", "tiger", "turtle", "viper",
	"walrus", "whale", "wolf", "wombat", "yak",
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Generate returns a random name in adjective-animal format.
func Generate() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	animal := animals[rand.Intn(len(animals))]
	return adj + "-" + animal
}
