import { createI18n } from 'vue-i18n'

// The admin web's four languages, as the panel's. No bare "@" or "|" in a
// message: vue-i18n reads them as linked messages and plurals.

const en = {
  app: { name: 'Nexora Notif', tagline: 'Notices to your users' },
  nav: { dashboard: 'Dashboard', security: 'Security', signOut: 'Sign out', language: 'Language' },
  login: {
    title: 'Sign in',
    username: 'Username',
    password: 'Password',
    submit: 'Sign in',
    code: 'Code from your authenticator app',
    verify: 'Verify',
    back: 'Back',
    forgot: 'Lost the password? On the server: notif admin reset-password -user admin -pass …',
  },
  dashboard: {
    title: 'Dashboard',
    version: 'Version',
    database: 'Database',
    waiting: 'Not registered with a panel yet',
    waitingHint:
      "On the panel: Services → Addons → Add, with this addon's address and the claim code below. An addon installed from the panel's directory registers by itself.",
    claimCode: 'Claim code',
    registered: 'Registered',
    panel: 'Panel',
    panelVersion: 'Panel version',
    since: 'Since',
    accounts: 'Accounts on the panel',
    scopes: 'Permissions',
    panelError: 'The panel did not answer a read with this addon’s token',
    next: 'Messengers, notices and messages arrive in the next versions.',
  },
  security: {
    title: 'Security',
    password: 'Password',
    current: 'Current password',
    new: 'New password',
    newHint: 'At least 10 characters. Your other sessions end.',
    change: 'Change password',
    changed: 'The password is changed.',
    totp: 'Two-factor sign-in',
    totpOn: 'On: every sign-in asks for a code from your authenticator app.',
    totpOff: 'Off: the password alone signs in.',
    enable: 'Turn on',
    scan: 'Scan this with your authenticator app, or type the key, then enter the code it shows.',
    key: 'Key',
    confirm: 'Confirm',
    enabled: 'Two-factor sign-in is on.',
    disable: 'Turn off',
    disablePassword: 'Your password, to turn it off',
    disabled: 'Two-factor sign-in is off.',
  },
  common: { error: 'Error', cancel: 'Cancel', never: 'never' },
}

type Messages = typeof en

const fa: Messages = {
  app: { name: 'نکسورا نوتیف', tagline: 'اطلاع‌رسانی به کاربران' },
  nav: { dashboard: 'داشبورد', security: 'امنیت', signOut: 'خروج', language: 'زبان' },
  login: {
    title: 'ورود',
    username: 'نام کاربری',
    password: 'رمز',
    submit: 'ورود',
    code: 'کد برنامهٔ احراز هویت',
    verify: 'تأیید',
    back: 'بازگشت',
    forgot: 'رمز را فراموش کرده‌اید؟ روی سرور: notif admin reset-password -user admin -pass …',
  },
  dashboard: {
    title: 'داشبورد',
    version: 'نسخه',
    database: 'دیتابیس',
    waiting: 'هنوز در هیچ پنلی ثبت نشده',
    waitingHint:
      'در پنل: سرویس‌ها ← افزونه‌ها ← افزودن، با نشانی همین افزونه و کد ادعای زیر. افزونه‌ای که از فهرست افزونه‌های پنل نصب شود خودش ثبت می‌شود.',
    claimCode: 'کد ادعا',
    registered: 'ثبت‌شده',
    panel: 'پنل',
    panelVersion: 'نسخهٔ پنل',
    since: 'از',
    accounts: 'حساب‌های پنل',
    scopes: 'دسترسی‌ها',
    panelError: 'پنل به خواندن با توکن این افزونه جواب نداد',
    next: 'پیام‌رسان‌ها، اعلان‌ها و پیام‌ها در نسخه‌های بعدی می‌آیند.',
  },
  security: {
    title: 'امنیت',
    password: 'رمز',
    current: 'رمز فعلی',
    new: 'رمز تازه',
    newHint: 'دست‌کم ۱۰ نویسه. نشست‌های دیگرتان بسته می‌شوند.',
    change: 'تغییر رمز',
    changed: 'رمز عوض شد.',
    totp: 'ورود دومرحله‌ای',
    totpOn: 'روشن: هر ورود کدی از برنامهٔ احراز هویت می‌خواهد.',
    totpOff: 'خاموش: رمز به‌تنهایی وارد می‌کند.',
    enable: 'روشن کردن',
    scan: 'این را با برنامهٔ احراز هویت اسکن کنید یا کلید را وارد کنید، بعد کدی را که نشان می‌دهد بنویسید.',
    key: 'کلید',
    confirm: 'تأیید',
    enabled: 'ورود دومرحله‌ای روشن شد.',
    disable: 'خاموش کردن',
    disablePassword: 'رمزتان، برای خاموش کردن',
    disabled: 'ورود دومرحله‌ای خاموش شد.',
  },
  common: { error: 'خطا', cancel: 'انصراف', never: 'هرگز' },
}

const ru: Messages = {
  app: { name: 'Nexora Notif', tagline: 'Уведомления пользователям' },
  nav: { dashboard: 'Обзор', security: 'Безопасность', signOut: 'Выйти', language: 'Язык' },
  login: {
    title: 'Вход',
    username: 'Имя пользователя',
    password: 'Пароль',
    submit: 'Войти',
    code: 'Код из приложения-аутентификатора',
    verify: 'Проверить',
    back: 'Назад',
    forgot: 'Забыли пароль? На сервере: notif admin reset-password -user admin -pass …',
  },
  dashboard: {
    title: 'Обзор',
    version: 'Версия',
    database: 'База данных',
    waiting: 'Ещё не зарегистрирован в панели',
    waitingHint:
      'В панели: Сервисы → Дополнения → Добавить, с адресом этого дополнения и кодом ниже. Дополнение, установленное из каталога панели, регистрируется само.',
    claimCode: 'Код регистрации',
    registered: 'Зарегистрирован',
    panel: 'Панель',
    panelVersion: 'Версия панели',
    since: 'С',
    accounts: 'Аккаунтов в панели',
    scopes: 'Права',
    panelError: 'Панель не ответила на чтение с токеном этого дополнения',
    next: 'Мессенджеры, уведомления и сообщения появятся в следующих версиях.',
  },
  security: {
    title: 'Безопасность',
    password: 'Пароль',
    current: 'Текущий пароль',
    new: 'Новый пароль',
    newHint: 'Не меньше 10 символов. Другие сеансы завершатся.',
    change: 'Сменить пароль',
    changed: 'Пароль изменён.',
    totp: 'Двухфакторный вход',
    totpOn: 'Включён: каждый вход спрашивает код из приложения-аутентификатора.',
    totpOff: 'Выключен: достаточно пароля.',
    enable: 'Включить',
    scan: 'Отсканируйте это приложением-аутентификатором или введите ключ, затем введите показанный код.',
    key: 'Ключ',
    confirm: 'Подтвердить',
    enabled: 'Двухфакторный вход включён.',
    disable: 'Выключить',
    disablePassword: 'Ваш пароль, чтобы выключить',
    disabled: 'Двухфакторный вход выключен.',
  },
  common: { error: 'Ошибка', cancel: 'Отмена', never: 'никогда' },
}

const zh: Messages = {
  app: { name: 'Nexora Notif', tagline: '向用户发送通知' },
  nav: { dashboard: '概览', security: '安全', signOut: '退出', language: '语言' },
  login: {
    title: '登录',
    username: '用户名',
    password: '密码',
    submit: '登录',
    code: '身份验证器应用中的验证码',
    verify: '验证',
    back: '返回',
    forgot: '忘记密码？在服务器上运行：notif admin reset-password -user admin -pass …',
  },
  dashboard: {
    title: '概览',
    version: '版本',
    database: '数据库',
    waiting: '尚未在面板中注册',
    waitingHint:
      '在面板中：服务 → 插件 → 添加，填写本插件的地址和下方的注册码。从面板插件目录安装的插件会自行注册。',
    claimCode: '注册码',
    registered: '已注册',
    panel: '面板',
    panelVersion: '面板版本',
    since: '注册于',
    accounts: '面板中的账户',
    scopes: '权限',
    panelError: '面板未响应使用本插件令牌的读取请求',
    next: '消息渠道、通知和消息将在后续版本中提供。',
  },
  security: {
    title: '安全',
    password: '密码',
    current: '当前密码',
    new: '新密码',
    newHint: '至少 10 个字符。您的其他会话将结束。',
    change: '修改密码',
    changed: '密码已修改。',
    totp: '两步验证登录',
    totpOn: '已开启：每次登录都需要身份验证器应用中的验证码。',
    totpOff: '已关闭：仅凭密码即可登录。',
    enable: '开启',
    scan: '用身份验证器应用扫描此码或输入密钥，然后填写其显示的验证码。',
    key: '密钥',
    confirm: '确认',
    enabled: '两步验证登录已开启。',
    disable: '关闭',
    disablePassword: '输入密码以关闭',
    disabled: '两步验证登录已关闭。',
  },
  common: { error: '错误', cancel: '取消', never: '从未' },
}

export const locales = [
  { code: 'fa', name: 'فارسی', rtl: true },
  { code: 'en', name: 'English', rtl: false },
  { code: 'ru', name: 'Русский', rtl: false },
  { code: 'zh', name: '中文', rtl: false },
] as const

export type Locale = (typeof locales)[number]['code']

function initial(): Locale {
  try {
    const saved = localStorage.getItem('notif.locale')
    if (locales.some((l) => l.code === saved)) return saved as Locale
  } catch {
    // storage blocked
  }
  const nav = navigator.language.slice(0, 2)
  return (locales.find((l) => l.code === nav)?.code ?? 'en') as Locale
}

export const i18n = createI18n({
  legacy: false,
  locale: initial(),
  fallbackLocale: 'en',
  messages: { en, fa, ru, zh },
})

export function setLocale(code: Locale) {
  i18n.global.locale.value = code
  try {
    localStorage.setItem('notif.locale', code)
  } catch {
    // storage blocked
  }
  applyDirection()
}

export function applyDirection() {
  const l = locales.find((x) => x.code === i18n.global.locale.value)
  document.documentElement.lang = l?.code ?? 'en'
  document.documentElement.dir = l?.rtl ? 'rtl' : 'ltr'
}

applyDirection()
