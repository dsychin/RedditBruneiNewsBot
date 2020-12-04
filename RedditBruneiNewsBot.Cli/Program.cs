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
using System.Net.Http;
using System.Text.RegularExpressions;

namespace RedditBruneiNewsBot.cli
{
    class Program
    {
        private static readonly string _version = "v0.2.0";
        private static List<Subreddit> _subreddits { get; set; } = new List<Subreddit>();
        private static readonly HttpClient _httpClient = new HttpClient();
        private static RedditClient _reddit { get; set; }

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

            _reddit = new RedditClient(
                redditConfig.AppId, redditConfig.RefreshToken, redditConfig.Secret);

            Console.WriteLine($"Logged in as: {_reddit.Account.Me.Name}");

            while (true)
            {
                Console.WriteLine("Enter permalink, or hit the Enter key to exit.");
                var permalink = Console.ReadLine();

                if (string.IsNullOrWhiteSpace(permalink))
                {
                    Console.WriteLine("Exiting program...");
                    break;
                }

                CheckPost(FromPermalink(permalink));
            }
        }

        private static Post FromPermalink(string permalink)
        {
            // Get the ID from the permalink, then preface it with "t3_" to convert it to a Reddit fullname.  --Kris
            Match match = Regex.Match(permalink, @"\/comments\/([a-z0-9]+)\/");

            string postFullname = "t3_" + (match != null && match.Groups != null && match.Groups.Count >= 2
                ? match.Groups[1].Value
                : "");
            if (postFullname.Equals("t3_"))
            {
                throw new Exception("Unable to extract ID from permalink.");
            }

            // Retrieve the post and return the result.  --Kris
            return _reddit.Post(postFullname).About();
        }

        private static void CheckPost(Post post)
        {
            Console.WriteLine("Found Post by " + post.Author + ": " + post.Title);
            if (!post.Listing.IsSelf)
            {
                var linkPost = (LinkPost)post;
                var uri = new Uri(linkPost.URL);
                var builder = new StringBuilder();
                var isSupported = true;

                try
                {
                    switch (uri.Authority)
                    {
                        case "borneobulletin.com.bn":
                        case "www.borneobulletin.com.bn":
                            builder = GetBorneoBulletinArticle(uri);
                            break;
                        case "thescoop.co":
                        case "www.thescoop.co":
                            builder = GetTheScoopArticle(uri);
                            break;
                        default:
                            isSupported = false;
                            break;
                    }

                    // post reply
                    if (isSupported)
                    {
                        // add footer
                        builder.AppendLine("***");
                        builder.Append($"^([ )[^(Give feedback)](https://www.reddit.com/message/compose?to=brunei_news_bot)^( | )[^(Code)](https://github.com/dsychin/RedditBruneiNewsBot)^( ] {_version})");

                        var reply = linkPost.Reply(builder.ToString());
                        Console.WriteLine($"Replied: {reply.Permalink}");
                    }
                }
                catch (Exception ex)
                {
                    Console.WriteLine("An error occured for the following post.");
                    Console.WriteLine($"    Post: {linkPost.Permalink}");
                    Console.WriteLine($"    Title: {linkPost.Title}");
                    Console.WriteLine($"    URL: {linkPost.URL}");
                    Console.WriteLine(ex.ToString());
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
            builder.Append($"^({date})\n\n");

            // add content
            foreach (var line in contentNode.ChildNodes)
            {
                builder.Append(line.InnerText + "\n\n");
            }

            return builder;
        }

        private static StringBuilder GetTheScoopArticle(Uri uri)
        {
            var stringTask = _httpClient.GetStringAsync(uri.ToString());
            var doc = new HtmlDocument();
            doc.LoadHtml(stringTask.Result);

            var contentNode = doc.QuerySelector(".post-content");

            // remove images and captions
            var figures = contentNode.QuerySelectorAll("figure,div");
            foreach (var figure in figures)
            {
                figure.Remove();
            }

            // build output text
            var builder = new StringBuilder();

            // add title
            var title = doc.QuerySelector(".post-title h1 .entry-title-primary").InnerText;
            builder.Append($"# {title}\n\n");

            // add date
            var date = doc.QuerySelector(".post-date").InnerText;
            builder.Append($"^({date.Trim()})\n\n");

            // add author
            var author = doc.QuerySelector(".scoop-byline").InnerText;
            builder.Append($"{author}\n\n");

            // add content
            foreach (var line in contentNode.ChildNodes)
            {
                builder.Append(line.InnerText + "\n\n");
            }

            return builder;
        }
    }
}
