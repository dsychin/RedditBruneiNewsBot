﻿using HtmlAgilityPack;
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
        private static readonly string _version = "v0.1.1";
        private static List<Subreddit> _subreddits { get; set; } = new List<Subreddit>();

        static void Main(string[] args)
        {
            Console.WriteLine($"Reddit Brunei News Bot {_version}");

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

            Console.WriteLine($"Monitoring the following subreddits: {string.Join(", ", subredditsToMonitor)}");

            foreach (var subredditName in subredditsToMonitor)
            {
                var subreddit = reddit.Subreddit(subredditName);
                _subreddits.Add(subreddit);

                var posts = new List<Post>();

                // start monitoring
                posts = subreddit.Posts.GetNew();
                subreddit.Posts.NewUpdated += NewPostUpdated;
                subreddit.Posts.MonitorNew();
            }

            Console.WriteLine("Program running. Press enter to stop.");
            Console.ReadLine();

            // stop monitoring
            // foreach (var subreddit in _subreddits)
            // {
            //     subreddit.Posts.MonitorNew();
            //     subreddit.Posts.NewUpdated -= NewPostUpdated;
            // }
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

                    try
                    {
                        switch (uri.Authority)
                        {
                            case "borneobulletin.com.bn":
                            case "www.borneobulletin.com.bn":
                                StringBuilder builder = GetBorneoBulletinArticle(uri);

                                // add footer
                                builder.AppendLine("***");
                                builder.Append($"^([ )[^(Give feedback)](https://www.reddit.com/message/compose?to=brunei_news_bot)^( | )[^(Code)](https://github.com/dsychin/RedditBruneiNewsBot)^( ] {_version})");
                                
                                // post reply
                                linkPost.Reply(builder.ToString());
                                break;
                        }
                        
                    }
                    catch (Exception)
                    {
                        Console.WriteLine("An error occured for the following post.");
                        Console.WriteLine($"    Post: {linkPost.Permalink}");
                        Console.WriteLine($"    Title: {linkPost.Title}");
                        Console.WriteLine($"    URL: {linkPost.URL}");
                    }

                }
            }
        }

        private static StringBuilder GetBorneoBulletinArticle(Uri uri)
        {
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

            // add title
            var title = doc.QuerySelector(".td-post-title h1").InnerText;
            builder.Append($"# {title}\n\n");

            // add date
            var date = doc.QuerySelector(".td-post-title time").InnerText;
            builder.Append($"^({date})");

            // add content
            foreach (var line in contentNode.ChildNodes)
            {
                builder.Append(line.InnerText + "\n\n");
            }

            return builder;
        }
    }
}
