package reddit

import "github.com/vartanbeno/go-reddit/v2/reddit"

type RedditClient interface {
	MonitorPosts(subreddits []string, callback func()) error
}

type NetworkRedditClient struct {
	client *reddit.Client
}

func (r *NetworkRedditClient) MonitorPosts(subreddits []string, callback func()) (<-chan *reddit.Post, <-chan error, []func()) {
	out := make(chan *reddit.Post)
	errOut := make(chan error)
	stops := make([](func()), len(subreddits))

	for i, s := range subreddits {
		postChan, errChan, stop := r.client.Stream.Posts(s)
		stops[i] = stop

		// forward post result to output channel
		go func(c <-chan *reddit.Post) {
			for v := range c {
				out <- v
			}
		}(postChan)

		// forward errors to error output channel
		go func(c <-chan error) {
			for v := range c {
				errOut <- v
			}
		}(errChan)
	}

	return out, errOut, stops
}
