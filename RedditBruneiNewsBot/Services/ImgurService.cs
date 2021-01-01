using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Text.Json;
using System.Threading.Tasks;

namespace RedditBruneiNewsBot.Services
{
    public class ImgurService : IDisposable
    {
        private readonly HttpClient _httpClient = new HttpClient();
        private readonly string _apiUrl = "https://api.imgur.com/3/";
        private readonly string _clientId;

        public ImgurService(string clientId)
        {
            _clientId = clientId;
        }

        public void Dispose()
        {
            _httpClient.Dispose();
        }

        public async Task<string> CreateAlbumFromImagesAsync(IEnumerable<Images> images)
        {
            var albumId = await CreateAlbumAsync();
            Console.WriteLine(albumId);
            return "https://imgur.com/a/" + albumId;
        }

        private async Task<string> CreateAlbumAsync()
        {
            var request = new HttpRequestMessage
            {
                RequestUri = new Uri(_apiUrl + "album"),
                Method = HttpMethod.Post,
                Headers = {
                    {
                        HttpRequestHeader.Authorization.ToString(),
                        "Client-ID " + _clientId
                    }
                }
            };

            var response = await _httpClient.SendAsync(request);
            response.EnsureSuccessStatusCode();
            var jsonString = await response.Content.ReadAsStringAsync();

            var responseObject = JsonSerializer
                .Deserialize<BasicResponse<AlbumCreationData>>(
                    jsonString,
                    new JsonSerializerOptions
                    {
                        PropertyNameCaseInsensitive = true
                    });

            return responseObject.Data.Id;
        }

        private class BasicResponse<T>
        {
            public T Data { get; set; }
            public bool Success { get; set; }
            public HttpStatusCode Status { get; set; }
        }

        private class AlbumCreationData
        {
            public string Id { get; set; }
            public string DeleteHash { get; set; }
        }
    }
}