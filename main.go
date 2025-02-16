package main

import (
	"fmt"

	"github.com/dsychin/RedditBruneiNewsBot/reddit"
	goreddit "github.com/vartanbeno/go-reddit/v2/reddit"
)

func main() {
	c := reddit.NewNetworkRedditClient()
	c.MonitorPosts([]string{"testingground4bots"}, func(post *goreddit.Post) {
		fmt.Println("found post", post.Title, post.Created)
	})

	for {
	}
}
