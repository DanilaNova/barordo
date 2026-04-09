if CLIENT then return end

Barordo = {
	VERSION = "0.1",
	Status = "",
	UpdatePeriod = 30,
	BotAddress = "http://127.0.0.1:8080",
	---@type string
	Path = table.pack(...)[1],
	---@type any
	LastError = nil,
	ErrorStrikes = 0,
	MaxErrorStrikes = 3,
}

---@module "json"
local json = dofile(Barordo.Path.."/Lua/json.lua")

---@enum HttpStatusCode
local statusCodes = {
	OK = 200,
}

local function log(...)
	print("[Barordo]: ", ...)
end

---@param method string
---@param address string
---@param callback HttpCallback?
---@param body string?
---@param headers table<string, string>?
local function http_request(method, address, callback, body, headers)
	---@param body string
	Networking.HttpRequest(address, function (body, statusCode, headers)
			if statusCode ~= statusCodes.OK then
				log(("%s request anomaly (status code %s): %s"):format(method, tostring(statusCode), body))
			end
			if callback ~= nil then callback(body, statusCode, headers) end
		end, body, method, "application/json", headers)
end

---@param callback HttpCallback
function Barordo:Ping(callback)
	http_request("GET", Barordo.BotAddress.."/status", callback)
end

local pingTime = os.clock()
Barordo:Ping(function (_, statusCode)
	if statusCode == statusCodes.OK then
		log(("Connected to server, ping: %.2f ms"):format((os.clock() - pingTime) * 1000))
	else
		Barordo.ErrorStrikes = Barordo.ErrorStrikes + 1
	end
end)

---@param status table
---@param callback HttpCallback
function Barordo:SetStatus(status, callback)
	http_request("POST", Barordo.BotAddress.."/status", function (body, statusCode, headers)
		if statusCode ~= statusCodes.OK then
			log(("Post request anomaly (status code %s): %s"):format(statusCode, body))
		end
		callback(body, statusCode, headers)
	end, json.encode(status))
end

local last_update = 0
Hook.Add("think", "barordo_think", function ()
	if Barordo.ErrorStrikes >= Barordo.MaxErrorStrikes then return end
	if (os.time() - last_update) < Barordo.UpdatePeriod then return end
	last_update = os.time()

	http_request("GET", Barordo.BotAddress.."/status",
		---@param body string
		function (body, statusCode, headers)
			if statusCode ~= statusCodes.OK then
				Barordo.LastError = {StatusCode = statusCode, Body = body, Headers = headers}

				Barordo.ErrorStrikes = Barordo.ErrorStrikes + 1
				if Barordo.ErrorStrikes >= Barordo.MaxErrorStrikes then
					log("Reached maximum number of error strikes, stopping updates until reset")
				end

				return
			end

			Barordo.Status = json.decode(body)
		end)
end)

log("Running.")