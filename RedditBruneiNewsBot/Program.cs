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
using System.Threading.Tasks;
using System.Linq;
using RedditBruneiNewsBot.Services;
using System.Text.RegularExpressions;

namespace RedditBruneiNewsBot
{
    class Program
    {
        private static readonly string _version = "v0.2.2";
        private static readonly int _maxRetries = 5;
        private static readonly int _retryInterval = 60000;
        private static List<Subreddit> _subreddits { get; set; } = new List<Subreddit>();
        private static ImgurService _imgurService;

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
            _imgurService = new ImgurService(configuration["Imgur:ClientId"]);

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

            Console.WriteLine("Program running. Press Ctrl+C to stop.");
        }

        private static async void NewPostUpdated(object sender, PostsUpdateEventArgs e)
        {
            foreach (Post post in e.Added)
            {
                Console.WriteLine("New Post by " + post.Author + ": " + post.Title);
                if (!post.Listing.IsSelf)
                {
                    var linkPost = (LinkPost)post;

                    var success = false;
                    var retries = 0;

                    while (!success && retries <= _maxRetries)
                    {
                        try
                        {
                            var uri = new Uri(linkPost.URL);
                            var builder = new StringBuilder();
                            var isSupported = true;

                            switch (uri.Authority)
                            {
                                case "borneobulletin.com.bn":
                                case "www.borneobulletin.com.bn":
                                    Console.WriteLine("Found Borneo Bulletin");
                                    builder = await GetBorneoBulletinArticle(uri);
                                    break;
                                case "thescoop.co":
                                case "www.thescoop.co":
                                    Console.WriteLine("Found The Scoop");
                                    builder = await GetTheScoopArticle(uri);
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
                            success = true;
                        }
                        catch (Exception ex)
                        {
                            retries++;
                            Console.WriteLine("An error occured for the following post.");
                            Console.WriteLine($"    Post: {linkPost.Permalink}");
                            Console.WriteLine($"    Title: {linkPost.Title}");
                            Console.WriteLine($"    URL: {linkPost.URL}");
                            Console.WriteLine(ex.ToString());

                            if (retries <= _maxRetries)
                            {
                                Console.WriteLine($"Retrying in {_retryInterval / 1000} seconds.");
                                await Task.Delay(_retryInterval);
                            }
                        }
                    }
                }
            }
        }

        private static async Task<StringBuilder> GetBorneoBulletinArticle(Uri uri)
        {
            using var httpClient = new HttpClient();
            var response = await httpClient.GetAsync(uri.ToString());
            response.EnsureSuccessStatusCode();
            var doc = new HtmlDocument();
            doc.LoadHtml(await response.Content.ReadAsStringAsync());

            var contentNode = doc.QuerySelector(".td-post-content");

            // remove images and captions from doc
            var images = new List<Image>();
            var figures = contentNode.QuerySelectorAll("figure");
            foreach (var figure in figures)
            {
                // get images in article
                var img = figure.QuerySelector("img");
                var imgSrcSet = img.GetAttributeValue("srcset", "");

                // get url from srcset
                var pattern = @"(https://\S+)";
                var match = Regex.Match(imgSrcSet, pattern, RegexOptions.RightToLeft);
                var imgUrl = match.Value;

                var caption = figure.QuerySelector("figcaption").InnerText.Trim();

                images.Add(new Image()
                {
                    Url = imgUrl,
                    Caption = caption
                });
                figure.Remove();
            }

            // Add images to Imgur
            var albumLink = "";
            if (images.Any())
            {
                try
                {
                    albumLink = await _imgurService.CreateAlbumFromImagesAsync(images);
                }
                catch (Exception ex)
                {
                    Console.WriteLine($"Error creating image album!\n{uri.ToString()}");
                    Console.WriteLine(ex.ToString());
                }
            }

            // build output text
            var builder = new StringBuilder();

            // add title
            var title = doc.QuerySelector(".td-post-title h1").InnerText;
            builder.Append($"# {title}\n\n");

            // add date
            var date = doc.QuerySelector(".td-post-title time").InnerText;
            builder.Append($"^({date})\n\n");

            // add link to image album
            if (!string.IsNullOrWhiteSpace(albumLink))
            {
                builder.Append($"[View Images]({albumLink})\n\n");
            }

            // add content
            foreach (var line in contentNode.ChildNodes)
            {
                builder.Append(line.InnerText + "\n\n");
            }

            return builder;
        }

        private static async Task<StringBuilder> GetTheScoopArticle(Uri uri)
        {
            using var httpClient = new HttpClient();
            var response = await httpClient.GetAsync(uri.ToString());
            response.EnsureSuccessStatusCode();
            var doc = new HtmlDocument();
            doc.LoadHtml(await response.Content.ReadAsStringAsync());

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
