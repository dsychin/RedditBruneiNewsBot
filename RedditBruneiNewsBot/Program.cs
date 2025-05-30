﻿using HtmlAgilityPack;
using HtmlAgilityPack.CssSelectors.NetCore;
using Microsoft.Extensions.Configuration;
using MihaZupan;
using Reddit;
using Reddit.Controllers;
using Reddit.Controllers.EventArgs;
using RedditBruneiNewsBot.Models;
using RedditBruneiNewsBot.Services;
using System;
using System.Collections.Generic;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.RegularExpressions;
using System.Text.Json;
using System.Threading.Tasks;

namespace RedditBruneiNewsBot
{
    public class CFClearanceResponse
    {
        public string Source { get; set; }
        public int Code { get; set; }
    }

    class Program
    {
        private static readonly string _version = "v0.5.2";
        private static readonly int _maxRetries = 15;
        private static readonly int _retryInterval = 30000;
        private static List<Subreddit> _subreddits { get; set; } = new List<Subreddit>();
        private static ImgurService _imgurService;
        private static string _proxyHost;
        private static int _proxyPort;
        private static string _proxyUsername;
        private static string _proxyPassword;

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

            // get reddit config
            var redditConfig = configuration.GetSection("Reddit").Get<RedditConfig>();

            var reddit = new RedditClient(
                redditConfig.AppId, redditConfig.RefreshToken, redditConfig.Secret);

            // get imgur config
            _imgurService = new ImgurService(configuration["Imgur:ClientId"]);

            // get proxy config
            try
            {
                _proxyHost = configuration["Proxy:Host"];
                _proxyPort = Int32.Parse(configuration["Proxy:Port"]);
                _proxyUsername = configuration["Proxy:Username"];
                _proxyPassword = configuration["Proxy:Password"];
            }
            catch (System.Exception)
            {
                _proxyHost = "";
                Console.WriteLine("Failed to get proxy from configuration. Using direct connection.");
            }

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
                if (post.Created <= DateTime.UtcNow.AddDays(-1))
                {
                    Console.WriteLine("Ignoring post created older than 1 day.");
                    break;
                }

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
                                case "thebruneian.news":
                                case "www.thebruneian.news":
                                    Console.WriteLine("Found The Bruneian News");
                                    builder = await GetTheBruneianNewsArticle(uri);
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
                                builder.Append($@"^([ )[^(Give feedback)](https://www.reddit.com/message/compose?to=brunei_news_bot)
                                ^( | )[^(Code)](https://github.com/dsychin/RedditBruneiNewsBot)
                                ^( | )[^(Changelog)](https://github.com/dsychin/RedditBruneiNewsBot/releases)
                                ^( ] {_version})");

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

            var requestUrl = "http://localhost:3000/cf-clearance-scraper";

            var requestBody = new
            {
                url = uri.ToString(),
                mode = "source",
                // NOTE: currently disabled due to proxy IPs being blocked
                // proxy = new
                // {
                //     host = _proxyHost,
                //     port = _proxyPort,
                //     username = _proxyUsername,
                //     password = _proxyPassword
                // }
            };

            var json = JsonSerializer.Serialize(requestBody);
            var content = new StringContent(json, Encoding.UTF8, "application/json");

            var response = await httpClient.PostAsync(requestUrl, content);

            response.EnsureSuccessStatusCode();

            var responseString = await response.Content.ReadAsStringAsync();
            var options = new JsonSerializerOptions{
                PropertyNamingPolicy = JsonNamingPolicy.CamelCase
            };
            var responseJson = JsonSerializer.Deserialize<CFClearanceResponse>(responseString, options);

            if (responseJson.Code != 200)
            {
                throw new Exception("Error from CF Clearance: " + responseString);
            }

            var doc = new HtmlDocument();
            doc.LoadHtml(responseJson.Source);

            // check for paywall
            var paywall = doc.QuerySelector(".leaky_paywall_message_wrap");
            if (paywall != null)
            {
                throw new Exception("Paywall detected!");
            }

            var contentNode = doc.QuerySelector(".td-post-content .tdb-block-inner");

            // remove images and captions from doc
            var images = new List<Image>();
            var figures = contentNode.QuerySelectorAll("figure");
            foreach (var figure in figures)
            {
                // get images in article
                var img = figure.QuerySelector("img");
                var imgSrcSet = img.GetAttributeValue("srcset", "");
                var bestUrl = GetBestUrlFromSrcset(imgSrcSet);

                var caption = figure.QuerySelector("figcaption").InnerText.Trim();

                if (!string.IsNullOrWhiteSpace(bestUrl))
                {
                    images.Add(new Image()
                    {
                        Url = bestUrl,
                        Caption = caption
                    });
                }
                figure.Remove();
            }

            // Get any remaining images not in figure element
            var imgNodes = contentNode.QuerySelectorAll("img");
            foreach (var img in imgNodes)
            {
                // get url from srcset
                var imgSrcSet = img.GetAttributeValue("srcset", "");
                var bestUrl = GetBestUrlFromSrcset(imgSrcSet);

                if (!string.IsNullOrWhiteSpace(bestUrl))
                {
                    images.Add(new Image()
                    {
                        Url = bestUrl,
                        Caption = ""
                    });
                }
                img.Remove();
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
            var title = doc.QuerySelector(".tdb-title-text").InnerText;
            builder.Append($"# {title}\n\n");

            // add date
            var date = doc.QuerySelector("time.entry-date").InnerText;
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

        private static async Task<StringBuilder> GetTheBruneianNewsArticle(Uri uri)
        {
            // set up proxy
            IWebProxy proxy = null;
            if (!string.IsNullOrWhiteSpace(_proxyHost))
            {
                proxy = new HttpToSocks5Proxy(_proxyHost, _proxyPort, _proxyUsername, _proxyPassword);
            }
            var handler = new HttpClientHandler { Proxy = proxy };

            using var httpClient = new HttpClient(handler, true);

            var response = await httpClient.GetAsync(uri.ToString());
            response.EnsureSuccessStatusCode();
            var doc = new HtmlDocument();
            doc.LoadHtml(await response.Content.ReadAsStringAsync());

            var contentNode = doc.QuerySelector(".entry-content");

            // remove ad space
            var ads = contentNode.QuerySelectorAll("div.code-block");
            foreach (var ad in ads)
            {
                ad.Remove();
            }

            // remove view counter
            var viewCounter = contentNode.QuerySelectorAll(".post-views");
            foreach (var view in viewCounter)
            {
                view.Remove();
            }

            // remove images and captions from doc
            var images = new List<Image>();
            var figures = contentNode.QuerySelectorAll("figure");
            foreach (var figure in figures)
            {
                // get images in article
                var img = figure.QuerySelector("img");
                var imgSrcSet = img.GetAttributeValue("srcset", "");
                var bestUrl = GetBestUrlFromSrcset(imgSrcSet);

                var figcaption = figure.QuerySelector("figcaption");
                var caption = "";
                if (figcaption != null)
                {
                    caption = figcaption.InnerText.Trim();
                }

                if (!string.IsNullOrWhiteSpace(bestUrl))
                {
                    images.Add(new Image()
                    {
                        Url = bestUrl,
                        Caption = caption
                    });
                }
                figure.Remove();
            }

            // Get any remaining images not in figure element
            var imgNodes = contentNode.QuerySelectorAll("img");
            foreach (var img in imgNodes)
            {
                // get url from srcset
                var imgSrcSet = img.GetAttributeValue("srcset", "");
                var bestUrl = GetBestUrlFromSrcset(imgSrcSet);

                if (!string.IsNullOrWhiteSpace(bestUrl))
                {
                    images.Add(new Image()
                    {
                        Url = bestUrl,
                        Caption = ""
                    });
                }
                img.Remove();
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
            var title = doc.QuerySelector("h1.headline").InnerText;
            builder.Append($"# {title}\n\n");

            // add date
            var date = doc.QuerySelector("span.entry-date").InnerText;
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

        private static string GetBestUrlFromSrcset(string imgSrcSet)
        {
            var pattern = @"(https://\S+) (\d+)w";
            var matches = Regex.Matches(imgSrcSet, pattern);
            var bestUrl = matches
                .OrderByDescending(x => Int32.Parse(x.Groups[2].Value))
                .Select(x => x.Groups[1].Value)
                .FirstOrDefault();
            return bestUrl;
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
