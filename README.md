# Reddit Brunei News Bot

![GitHub release (latest by date)](https://img.shields.io/github/v/release/dsychin/RedditBruneiNewsBot)
![GitHub code size in bytes](https://img.shields.io/github/languages/code-size/dsychin/RedditBruneiNewsBot)
![GitHub issues](https://img.shields.io/github/issues-raw/dsychin/RedditBruneiNewsBot)
![GitHub last commit](https://img.shields.io/github/last-commit/dsychin/RedditBruneiNewsBot)
![.NET Core Build](https://github.com/dsychin/RedditBruneiNewsBot/workflows/.NET%20Core%20Build/badge.svg)

## Description

This repository includes the code for the `brunei_news_bot` reddit account which monitors a list of subreddit for new post with links to news articles and automatically scrape the website for the content and post it as a reply.

## Features

1. Supported Websites:
    - [Borneo Bulletin](https://www.borneobulletin.com.bn)
    - [The Scoop](https://thescoop.co)
2. Posts a comment reply with the news title, date and content.

## Running the bot

### Prerequisites

1. [.NET 5.0 SDK](https://dotnet.microsoft.com/download)
2. Windows/Linux/Mac

### Developing locally

1. Create `appsettings.json` in project folder with the following structure.
```
{
    "Reddit": {
        "Secret": "",
        "AppId": "",
        "RefreshToken": ""
    },
    "Imgur": {
        "ClientId": ""
    },
    "Proxy": {
        "Host": "",
        "Port": 80,
        "Username": "",
        "Password": ""
    },
    "Subreddits": "testingground4bots"
}
```

2. Run `dotnet run`.

### Creating release build

1. Open a terminal in the `RedditBruneiNewsBot` directory in this repository.
2. Run `dotnet build -c Release -o app`.
3. This will create a build in the `app` folder.
4. Build configuration can be customised. E.g. To create a single `.exe` file that runs on Windows, etc.
Please refer to the [Microsoft Docs](https://docs.microsoft.com/en-us/dotnet/core/tools/dotnet-build) for further information.

### Running on the server

1. Copy the `app` folder from the previous section to somewhere safe on the server.
2. Change working directory into the `app` folder.
3. Update `appsettings.json` or set it through environment variables. E.g.
```
Reddit__Secret=123
Reddit__AppId=123
Reddit__RefreshToken=123
Imgur__ClientId=123
Subreddits=123,456
```
4. Run `dotnet RedditBruneiNewsBot.dll`.
