---@meta

---@alias HttpCallback fun(body: string, statusCode?: HttpStatusCode, headers?: table<string, string>)

---@param callback HttpCallback
Networking.HttpGet = function(url, callback, headers, savePath) end