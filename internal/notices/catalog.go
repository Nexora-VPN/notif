package notices

// The notices Notif sends (GN-S4), in three families: what an event of the
// panel's says happened, what a read of the account found an admin changed,
// and what the admin's own schedule says is coming. Each is on or off, and
// its words are the admin's, per language and per channel kind, over these
// defaults — strictly transactional: no price, no offer, no word VPN (P40).

// Def is one kind of notice.
type Def struct {
	Kind   string `json:"kind"`
	Family string `json:"family"` // "event", "edit", "schedule" or "message"
	// On is whether it is sent before the admin says otherwise.
	On bool `json:"on"`
	// Vars are the variables its texts may use.
	Vars []string `json:"vars"`
}

var common = []string{"name", "group", "sub_url", "expiry", "days", "traffic_total", "traffic_used", "traffic_left"}

// Catalog is every kind, in the admin's order.
var Catalog = []Def{
	{Kind: "expiring", Family: "schedule", On: true, Vars: common},
	{Kind: "traffic_warning", Family: "schedule", On: true, Vars: append([]string{"percent"}, common...)},
	{Kind: "renewed", Family: "event", On: true, Vars: common},
	{Kind: "expired", Family: "event", On: true, Vars: common},
	{Kind: "quota_reached", Family: "event", On: true, Vars: common},
	{Kind: "created", Family: "event", On: true, Vars: common},
	{Kind: "disabled", Family: "event", On: true, Vars: common},
	{Kind: "restored", Family: "event", On: false, Vars: common},
	{Kind: "activated", Family: "event", On: false, Vars: common},
	{Kind: "first_fetch", Family: "event", On: false, Vars: common},
	{Kind: "device_limit", Family: "event", On: true, Vars: common},
	{Kind: "deleted", Family: "event", On: false, Vars: []string{"name", "group"}},
	{Kind: "traffic_added", Family: "edit", On: true, Vars: append([]string{"added"}, common...)},
	{Kind: "expiry_changed", Family: "edit", On: true, Vars: common},
	{Kind: "usage_reset", Family: "edit", On: true, Vars: common},
	{Kind: "admin_disabled", Family: "edit", On: true, Vars: common},
	{Kind: "admin_enabled", Family: "edit", On: true, Vars: common},
}

// Lookup is a kind's definition.
func Lookup(kind string) (Def, bool) {
	for _, d := range Catalog {
		if d.Kind == kind {
			return d, true
		}
	}
	return Def{}, false
}

type text struct{ title, body string }

// builtin are the default words, per kind and language.
var builtin = map[string]map[string]text{
	KindTest: {
		"en": {"Test", "Hello {name}, this is a test from {channel}. Notices about your account will arrive here."},
		"fa": {"آزمایش", "سلام {name}، این پیام آزمایشی از {channel} است. خبرهای حساب شما از همین راه می‌رسد."},
		"ru": {"Проверка", "Здравствуйте, {name}! Это проверка канала {channel}. Уведомления о вашем аккаунте будут приходить сюда."},
		"zh": {"测试", "{name}，您好，这是来自 {channel} 的测试消息。有关您账户的通知将通过这里发送。"},
	},
	"expiring": {
		"en": {"Your account expires soon", "{name}, your account expires in {days} days, on {expiry}. Contact us to renew it."},
		"fa": {"حساب شما به‌زودی تمام می‌شود", "{name}، حساب شما {days} روز دیگر، در {expiry}، تمام می‌شود. برای تمدید با ما در تماس باشید."},
		"ru": {"Срок аккаунта скоро истечёт", "{name}, срок вашего аккаунта истекает через {days} дн., {expiry}. Свяжитесь с нами, чтобы продлить его."},
		"zh": {"账户即将到期", "{name}，您的账户将在 {days} 天后（{expiry}）到期。如需续期请联系我们。"},
	},
	"traffic_warning": {
		"en": {"Your traffic is running low", "{name}, you have used {percent}% of your traffic: {traffic_left} left of {traffic_total}."},
		"fa": {"ترافیک شما رو به اتمام است", "{name}، {percent}٪ از ترافیک حسابتان مصرف شده: {traffic_left} از {traffic_total} باقی مانده."},
		"ru": {"Трафик заканчивается", "{name}, вы израсходовали {percent}% трафика: осталось {traffic_left} из {traffic_total}."},
		"zh": {"流量即将用完", "{name}，您已使用 {percent}% 的流量：{traffic_total} 中剩余 {traffic_left}。"},
	},
	"renewed": {
		"en": {"Account renewed", "{name}, your account was renewed. It is valid until {expiry}, with {traffic_total} of traffic."},
		"fa": {"حساب تمدید شد", "{name}، حساب شما تمدید شد. اعتبار تا {expiry}، با {traffic_total} ترافیک."},
		"ru": {"Аккаунт продлён", "{name}, ваш аккаунт продлён. Он действует до {expiry}, трафик — {traffic_total}."},
		"zh": {"账户已续期", "{name}，您的账户已续期，有效期至 {expiry}，流量 {traffic_total}。"},
	},
	"expired": {
		"en": {"Account expired", "{name}, your account expired on {expiry}. Contact us to renew it."},
		"fa": {"حساب منقضی شد", "{name}، مدت حساب شما در {expiry} تمام شد. برای تمدید با ما در تماس باشید."},
		"ru": {"Срок аккаунта истёк", "{name}, срок вашего аккаунта истёк {expiry}. Свяжитесь с нами, чтобы продлить его."},
		"zh": {"账户已到期", "{name}，您的账户已于 {expiry} 到期。如需续期请联系我们。"},
	},
	"quota_reached": {
		"en": {"Traffic used up", "{name}, your account has used all {traffic_total} of its traffic. Contact us to add more."},
		"fa": {"ترافیک تمام شد", "{name}، همهٔ {traffic_total} ترافیک حساب شما مصرف شده. برای افزایش با ما در تماس باشید."},
		"ru": {"Трафик исчерпан", "{name}, ваш аккаунт израсходовал весь трафик ({traffic_total}). Свяжитесь с нами, чтобы добавить ещё."},
		"zh": {"流量已用完", "{name}，您的账户已用完全部 {traffic_total} 流量。如需增加请联系我们。"},
	},
	"created": {
		"en": {"Your account is ready", "Hello {name}, your account is ready: valid until {expiry}, with {traffic_total} of traffic. Your subscription link: {sub_url}"},
		"fa": {"حساب شما آماده است", "سلام {name}، حساب شما آماده است: اعتبار تا {expiry}، با {traffic_total} ترافیک. لینک اشتراک شما: {sub_url}"},
		"ru": {"Ваш аккаунт готов", "Здравствуйте, {name}! Ваш аккаунт готов: действует до {expiry}, трафик — {traffic_total}. Ваша ссылка на подписку: {sub_url}"},
		"zh": {"您的账户已就绪", "{name}，您好，您的账户已就绪：有效期至 {expiry}，流量 {traffic_total}。您的订阅链接：{sub_url}"},
	},
	"disabled": {
		"en": {"Account switched off", "{name}, your account has been switched off because its seller’s allowance ran out. Contact your seller."},
		"fa": {"حساب غیرفعال شد", "{name}، حساب شما غیرفعال شد چون سهمیهٔ فروشنده‌تان تمام شده. با فروشنده‌تان تماس بگیرید."},
		"ru": {"Аккаунт отключён", "{name}, ваш аккаунт отключён: закончился лимит вашего продавца. Свяжитесь с продавцом."},
		"zh": {"账户已停用", "{name}，由于您的销售商额度已用完，您的账户已停用。请联系您的销售商。"},
	},
	"restored": {
		"en": {"Account working again", "{name}, your account is working again."},
		"fa": {"حساب دوباره فعال شد", "{name}، حساب شما دوباره کار می‌کند."},
		"ru": {"Аккаунт снова работает", "{name}, ваш аккаунт снова работает."},
		"zh": {"账户已恢复", "{name}，您的账户已恢复使用。"},
	},
	"activated": {
		"en": {"Plan started", "{name}, your plan started with your first connection. It runs until {expiry}."},
		"fa": {"دورهٔ حساب شروع شد", "{name}، دورهٔ حساب شما با اولین اتصال شروع شد و تا {expiry} ادامه دارد."},
		"ru": {"Тариф начался", "{name}, ваш тариф начался с первого подключения и действует до {expiry}."},
		"zh": {"套餐已开始", "{name}，您的套餐已随首次连接开始，有效期至 {expiry}。"},
	},
	"first_fetch": {
		"en": {"Subscription added", "{name}, your subscription was added to an app. Notices about your account will arrive here."},
		"fa": {"اشتراک اضافه شد", "{name}، اشتراک شما به یک برنامه اضافه شد. خبرهای حسابتان از همین راه می‌رسد."},
		"ru": {"Подписка добавлена", "{name}, ваша подписка добавлена в приложение. Уведомления об аккаунте будут приходить сюда."},
		"zh": {"订阅已添加", "{name}，您的订阅已添加到应用中。账户通知将发送到这里。"},
	},
	"device_limit": {
		"en": {"Device limit reached", "{name}, a device was refused because your account’s device limit is full."},
		"fa": {"سقف دستگاه پر شد", "{name}، یک دستگاه پذیرفته نشد چون سقف دستگاه‌های حساب شما پر است."},
		"ru": {"Достигнут лимит устройств", "{name}, устройство не подключено: лимит устройств вашего аккаунта исчерпан."},
		"zh": {"设备数已达上限", "{name}，有一台设备被拒绝，因为您账户的设备数已达上限。"},
	},
	"deleted": {
		"en": {"Account deleted", "{name}, your account has been deleted."},
		"fa": {"حساب حذف شد", "{name}، حساب شما حذف شد."},
		"ru": {"Аккаунт удалён", "{name}, ваш аккаунт удалён."},
		"zh": {"账户已删除", "{name}，您的账户已被删除。"},
	},
	"traffic_added": {
		"en": {"Traffic added", "{name}, {added} of traffic was added to your account: {traffic_left} left of {traffic_total}."},
		"fa": {"ترافیک اضافه شد", "{name}، {added} ترافیک به حساب شما اضافه شد: {traffic_left} از {traffic_total} باقی است."},
		"ru": {"Трафик добавлен", "{name}, к вашему аккаунту добавлено {added} трафика: осталось {traffic_left} из {traffic_total}."},
		"zh": {"流量已增加", "{name}，您的账户已增加 {added} 流量：{traffic_total} 中剩余 {traffic_left}。"},
	},
	"expiry_changed": {
		"en": {"Expiry date changed", "{name}, your account is now valid until {expiry}."},
		"fa": {"تاریخ انقضا عوض شد", "{name}، اعتبار حساب شما اکنون تا {expiry} است."},
		"ru": {"Срок изменён", "{name}, ваш аккаунт теперь действует до {expiry}."},
		"zh": {"到期日已更改", "{name}，您的账户现在有效期至 {expiry}。"},
	},
	"usage_reset": {
		"en": {"Traffic reset", "{name}, your account’s traffic was reset: {traffic_left} available."},
		"fa": {"ترافیک از نو شروع شد", "{name}، مصرف ترافیک حساب شما صفر شد: {traffic_left} در دسترس است."},
		"ru": {"Трафик обнулён", "{name}, трафик вашего аккаунта обнулён: доступно {traffic_left}."},
		"zh": {"流量已重置", "{name}，您账户的流量已重置：可用 {traffic_left}。"},
	},
	"admin_disabled": {
		"en": {"Account switched off", "{name}, your account has been switched off. Contact us if you did not expect this."},
		"fa": {"حساب غیرفعال شد", "{name}، حساب شما غیرفعال شد. اگر انتظارش را نداشتید با ما در تماس باشید."},
		"ru": {"Аккаунт отключён", "{name}, ваш аккаунт отключён. Если вы этого не ожидали, свяжитесь с нами."},
		"zh": {"账户已停用", "{name}，您的账户已被停用。如有疑问请联系我们。"},
	},
	"admin_enabled": {
		"en": {"Account switched on", "{name}, your account is switched on again."},
		"fa": {"حساب فعال شد", "{name}، حساب شما دوباره فعال شد."},
		"ru": {"Аккаунт включён", "{name}, ваш аккаунт снова включён."},
		"zh": {"账户已启用", "{name}，您的账户已重新启用。"},
	},
}
