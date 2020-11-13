FROM mcr.microsoft.com/dotnet/aspnet:5.0 AS base
WORKDIR /app

FROM mcr.microsoft.com/dotnet/sdk:5.0 AS build
WORKDIR /src
COPY ["RedditBruneiNewsBot/RedditBruneiNewsBot.csproj", "RedditBruneiNewsBot/"]
RUN dotnet restore "RedditBruneiNewsBot/RedditBruneiNewsBot.csproj"
COPY . .
WORKDIR "/src/RedditBruneiNewsBot"
RUN dotnet build "RedditBruneiNewsBot.csproj" -c Release -o /app/build

FROM build AS publish
RUN dotnet publish "RedditBruneiNewsBot.csproj" -c Release -o /app/publish

FROM base AS final
WORKDIR /app
COPY --from=publish /app/publish .
ENTRYPOINT ["dotnet", "RedditBruneiNewsBot.dll"]
