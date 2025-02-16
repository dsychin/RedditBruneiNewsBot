package reddit

import (
	"fmt"

	"github.com/vartanbeno/go-reddit/v2/reddit"
)

type RedditClient interface {
	MonitorPosts(subreddits []string, callback func()) error
}

type NetworkRedditClient struct {
	client *reddit.Client
}

func NewNetworkRedditClient() *NetworkRedditClient {
	client, _ := reddit.NewClient(reddit.Credentials{}, reddit.FromEnv)
	return &NetworkRedditClient{
		client: client,
	}
}

func (r *NetworkRedditClient) MonitorPosts(subreddits []string, callback func(post *reddit.Post)) func() {
	stops := make([](func()), len(subreddits))

	for i, s := range subreddits {
		postChan, errChan, stop := r.client.Stream.Posts(s)
		stops[i] = stop

		// call callback function when post is received
		go func(c <-chan *reddit.Post) {
			for v := range c {
				callback(v)
			}
		}(postChan)

		// forward errors to error output channel
		go func(c <-chan error) {
			for v := range c {
				fmt.Printf("err: %+v", v)
			}
		}(errChan)
	}

	stopAll := func() {
		for _, stop := range stops {
			stop()
		}
	}

	return stopAll
}
