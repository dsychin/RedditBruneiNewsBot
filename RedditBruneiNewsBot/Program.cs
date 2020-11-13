using System;
using Microsoft.Extensions.Configuration;
using RedditBruneiNewsBot.Models;
using Reddit;
using System.Collections.Generic;
using Reddit.Controllers;
using Reddit.Controllers.EventArgs;

namespace RedditBruneiNewsBot
{
    class Program
    {
        static void Main(string[] args)
        {
            var environmentName = Environment.GetEnvironmentVariable("ASPNETCORE_ENVIRONMENT");

            var builder = new ConfigurationBuilder()
                .AddJsonFile($"appsettings.json", false, true)
                .AddJsonFile($"appsettings.{environmentName}.json", true, true)
                .AddEnvironmentVariables()
                .AddCommandLine(args);
            var configuration = builder.Build();

            var redditConfig = configuration.GetSection("Reddit").Get<RedditConfig>();

            var reddit = new RedditClient(
                redditConfig.AppId, redditConfig.RefreshToken, redditConfig.Secret);

            Console.WriteLine($"Logged in as: {reddit.Account.Me.Name}");

            var subreddit = reddit.Subreddit("testingground4bots");

            var posts = new List<Post>();

            // start monitoring
            posts = subreddit.Posts.GetNew();
            subreddit.Posts.NewUpdated += NewPostUpdated;
            subreddit.Posts.MonitorNew();

            Console.WriteLine("Program running. Press enter to stop.");
            Console.ReadLine();

            // stop monitoring
            subreddit.Posts.MonitorNew();
            subreddit.Posts.NewUpdated -= NewPostUpdated;
        }

        private static void NewPostUpdated(object sender, PostsUpdateEventArgs e)
        {
            foreach (Post post in e.Added)
            {
                Console.WriteLine("New Post by " + post.Author + ": " + post.Title);
                if (!post.Listing.IsSelf)
                {
                    Console.WriteLine("Link post: " + ((LinkPost) post).URL);

                    // post reply
                    post.Reply("Bot reply!\r\nI see a new link post: " + ((LinkPost) post).URL);
                }
            }
        }
    }
}
