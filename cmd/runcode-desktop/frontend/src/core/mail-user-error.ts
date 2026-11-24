// 把后端草稿校验的英文错误码翻成设置/邮件页可直接展示的中文说明。
// 匹配时去掉 "mail: invalid input:" 等前缀，按后缀关键字判定，避免整句漂移后失效。

const RULES: { re: RegExp; zh: string }[] = [
  { re: /empty subject/i, zh: '请填写主题' },
  { re: /subject too long/i, zh: '主题过长（最多 200 字）' },
  { re: /header injection in subject/i, zh: '主题含有非法换行或冒号，请修改后重试' },
  { re: /empty body/i, zh: '请填写正文内容' },
  { re: /body too large/i, zh: '正文过长，请删减后重试' },
  { re: /native HTML/i, zh: '正文不能包含 HTML 标签，请使用 Markdown 纯文本' },
  { re: /need recipients/i, zh: '请至少填写一位收件人' },
  { re: /empty address/i, zh: '收件人邮箱不能为空' },
  { re: /bad address/i, zh: '收件人邮箱格式不正确' },
  { re: /header injection/i, zh: '收件人姓名或地址含有非法字符' },
  { re: /too many addresses/i, zh: '收件人过多，请减少后重试' },
  { re: /individual forbids/i, zh: '分别发送模式不能填写抄送或密送' },
  { re: /account required/i, zh: '请先在设置中接入可用邮箱账号' },
  { re: /send mode/i, zh: '发送模式无效' },
  { re: /dispatch mode/i, zh: '投递模式无效' },
  { re: /schedule requires/i, zh: '定时发送需要填写本地时间与时区' },
  { re: /ambiguous contact/i, zh: '同名联系人有多条，请明确选择邮箱地址' },
  { re: /contact not found/i, zh: '未找到该联系人' },
]

/** 将邮件页/保存草稿等错误翻成用户可读中文；已是中文或未知文案原样返回。 */
export function formatMailUserError(raw: string): string {
  const text = (raw || '').trim()
  if (!text) return '操作失败'
  for (const { re, zh } of RULES) {
    if (re.test(text)) return zh
  }
  // 去掉常见英文前缀后若仍含中文，直接展示剩余部分。
  const stripped = text
    .replace(/^mail:\s*invalid input:\s*/i, '')
    .replace(/^mail:\s*/i, '')
    .trim()
  if (stripped && /[\u4e00-\u9fff]/.test(stripped)) return stripped
  return stripped || text
}
