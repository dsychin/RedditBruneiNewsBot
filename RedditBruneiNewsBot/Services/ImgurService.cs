using System;
using System.Collections.Generic;
using System.Linq;
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
            _httpClient = new HttpClient();
            _httpClient.DefaultRequestHeaders.Add(
                HttpRequestHeader.Authorization.ToString(),
                $"Client-ID {_clientId}"
            );
        }

        public void Dispose()
        {
            _httpClient.Dispose();
        }

        public async Task<string> CreateAlbumFromImagesAsync(IEnumerable<Image> images)
        {
            var albumData = await CreateAlbumAsync();
            await AddImagesToAlbumAsync(albumData.DeleteHash, images);
            return "https://imgur.com/a/" + albumData.Id;
        }

        private async Task AddImagesToAlbumAsync(string albumHash, IEnumerable<Image> images)
        {
            // upload images simultaneously
            var tasks = new List<Task<ImageUploadData>>();
            foreach (var image in images)
            {
                tasks.Add(UploadImageAsync(albumHash, image));
            }

            // wait for all task to complete
            await Task.WhenAll(tasks);

            // get all image data
            var imageDatas = new List<ImageUploadData>();
            foreach (var task in tasks)
            {
                imageDatas.Add(await task);
            }

            await UpdateAlbumWithImages(albumHash, imageDatas);
        }

        private async Task UpdateAlbumWithImages(string albumHash, List<ImageUploadData> imageDatas)
        {
            var imageHashes = imageDatas.Select(i => i.DeleteHash).ToList();
            var content = new MultipartFormDataContent();
            content.Add(new StringContent(string.Join(",", imageHashes)), "deletehashes");

            var request = new HttpRequestMessage
            {
                RequestUri = new Uri($"{_apiUrl}album/{albumHash}/add"),
                Method = HttpMethod.Post,
                Content = content
            };

            var response = await _httpClient.SendAsync(request);
            response.EnsureSuccessStatusCode();
            var jsonString = await response.Content.ReadAsStringAsync();
        }

        private async Task<ImageUploadData> UploadImageAsync(string albumHash, Image image)
        {
            // upload image
            var content = new MultipartFormDataContent();
            content.Add(new StringContent(image.Url), "image");
            content.Add(new StringContent("url"), "type");
            content.Add(new StringContent(image.Caption), "description");

            var request = new HttpRequestMessage
            {
                RequestUri = new Uri(_apiUrl + "upload"),
                Method = HttpMethod.Post,
                Content = content
            };

            var response = await _httpClient.SendAsync(request);
            response.EnsureSuccessStatusCode();
            var jsonString = await response.Content.ReadAsStringAsync();

            var responseObject = JsonSerializer
                .Deserialize<BasicResponse<ImageUploadData>>(
                    jsonString,
                    new JsonSerializerOptions
                    {
                        PropertyNameCaseInsensitive = true
                    });

            return responseObject.Data;
        }

        private async Task<AlbumCreationData> CreateAlbumAsync()
        {
            var request = new HttpRequestMessage
            {
                RequestUri = new Uri(_apiUrl + "album"),
                Method = HttpMethod.Post
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

            return responseObject.Data;
        }

        private class BasicResponse<T>
        {
            public T Data { get; set; }
            public bool Success { get; set; }
            public HttpStatusCode Status { get; set; }
        }

        private class ImageUploadData
        {
            public string Id { get; set; }
            public string DeleteHash { get; set; }
        }

        private class AlbumCreationData
        {
            public string Id { get; set; }
            public string DeleteHash { get; set; }
        }
    }
}