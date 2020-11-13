using HtmlAgilityPack;
using Microsoft.Extensions.Configuration;
using Reddit;
using Reddit.Controllers;
using Reddit.Controllers.EventArgs;
using RedditBruneiNewsBot.Models;
using System;
using System.Collections.Generic;
using HtmlAgilityPack.CssSelectors.NetCore;
using System.Text;

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

            var subredditsToMonitor = configuration["Subreddits"].Split(",");

            foreach (var subredditName in subredditsToMonitor)
            {
                var subreddit = reddit.Subreddit(subredditName);

                var posts = new List<Post>();

                // start monitoring
                posts = subreddit.Posts.GetNew();
                subreddit.Posts.NewUpdated += NewPostUpdated;
                subreddit.Posts.MonitorNew();
            }

            Console.WriteLine("Program running. Press enter to stop.");
            Console.ReadLine();

            // stop monitoring
            // subreddit.Posts.MonitorNew();
            // subreddit.Posts.NewUpdated -= NewPostUpdated;
        }

        private static void NewPostUpdated(object sender, PostsUpdateEventArgs e)
        {
            foreach (Post post in e.Added)
            {
                Console.WriteLine("New Post by " + post.Author + ": " + post.Title);
                if (!post.Listing.IsSelf)
                {
                    var linkPost = (LinkPost) post;
                    var uri = new Uri(linkPost.URL);

                    switch (uri.Authority)
                    {
                        case "borneobulletin.com.bn":
                        case "www.borneobulletin.com.bn":
                            var web = new HtmlWeb();
                            var doc = web.Load(uri.ToString());

                            var contentNode = doc.QuerySelector(".td-post-content");

                            // remove images and captions
                            var figures = contentNode.QuerySelectorAll("figure");
                            foreach (var figure in figures)
                            {
                                figure.Remove();
                            }

                            // build output text
                            var builder = new StringBuilder();
                            foreach (var line in contentNode.QuerySelectorAll("p"))
                            {
                                builder.Append(line.InnerText + "\n\n");
                            }

                            // post reply
                            post.Reply(builder.ToString());
                            break;
                    }

                }
            }
        }
    }
}
