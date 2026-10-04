package channel

// The generic channel's presets (P39 (a), P40 (a)): providers whose API
// fits one request, read from their documentation on 2026-10-04. The
// admin's own values — a sender name, a homeserver — are written in
// capitals; the provider's key goes in the secret field, which the
// templates read as {{.Secret}}. The address field names the contact key
// that holds the user's address there: the phone for SMS, and an
// operator's own key (vk_id, line_id, matrix_room, pushover_key) for the
// rest, filled in the panel's contact card.
var httpPresets = []Preset{
	{
		Name: "SMS.ru", Market: "ru",
		Config: map[string]string{
			"url": "https://sms.ru/sms/send", "method": "POST", "address": "phone", "bodyType": "form",
			"body":           "api_id={{.Secret}}\nto={{msisdn .Address}}\nmsg={{.Text}}\nfrom=SENDER\njson=1",
			"successPattern": `"sms_id"`,
		},
	},
	{
		Name: "SMSC.ru", Market: "ru",
		Config: map[string]string{
			"url": "https://smsc.ru/sys/send.php", "method": "POST", "address": "phone", "bodyType": "form",
			"body":           "apikey={{.Secret}}\nphones={{msisdn .Address}}\nmes={{.Text}}\nsender=SENDER\nfmt=3\ncharset=utf-8",
			"successPattern": `"id"`,
		},
	},
	{
		Name: "SMS Aero", Market: "ru",
		Config: map[string]string{
			"url": "https://gate.smsaero.ru/v2/sms/send", "method": "POST", "address": "phone", "bodyType": "form",
			"body":      "number={{msisdn .Address}}\ntext={{.Text}}\nsign=SENDER",
			"basicUser": "YOUR_ACCOUNT_EMAIL", "successPattern": `"success"\s*:\s*true`,
		},
	},
	{
		Name: "MTS Exolve", Market: "ru",
		Config: map[string]string{
			"url": "https://api.exolve.ru/messaging/v1/SendSMS", "method": "POST", "address": "phone", "bodyType": "json",
			"headers":        "Authorization: Bearer {{.Secret}}",
			"body":           `{"number": "YOUR_NUMBER", "destination": {{json (msisdn .Address)}}, "text": {{json .Text}}}`,
			"successPattern": `"message_id"`,
		},
	},
	{
		Name: "VK", Market: "ru",
		Config: map[string]string{
			"url": "https://api.vk.com/method/messages.send", "method": "POST", "address": "vk_id", "bodyType": "form",
			"body":           "access_token={{.Secret}}\nv=5.199\nuser_id={{.Address}}\nrandom_id={{.ID}}\nmessage={{.Title}}: {{.Text}}",
			"successPattern": `"response"`,
		},
	},
	{
		Name: "LINE",
		Config: map[string]string{
			"url": "https://api.line.me/v2/bot/message/push", "method": "POST", "address": "line_id", "bodyType": "json",
			"headers": "Authorization: Bearer {{.Secret}}",
			"body":    `{"to": {{json .Address}}, "messages": [{"type": "text", "text": {{json .Text}}}]}`,
		},
	},
	{
		Name: "Matrix",
		Config: map[string]string{
			"url":    "https://HOMESERVER/_matrix/client/v3/rooms/{{urlquery .Address}}/send/m.room.message/{{urlquery .Key}}",
			"method": "PUT", "address": "matrix_room", "bodyType": "json",
			"headers": "Authorization: Bearer {{.Secret}}",
			"body":    `{"msgtype": "m.text", "body": {{json .Text}}}`,
		},
	},
	{
		Name: "Pushover",
		Config: map[string]string{
			"url": "https://api.pushover.net/1/messages.json", "method": "POST", "address": "pushover_key", "bodyType": "form",
			"body":           "token={{.Secret}}\nuser={{.Address}}\ntitle={{.Title}}\nmessage={{.Text}}",
			"successPattern": `"status"\s*:\s*1`,
		},
	},
}
