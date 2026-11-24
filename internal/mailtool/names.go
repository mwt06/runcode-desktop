// Package mailtool 提供桌面专属邮件模型工具：search_contacts（只读）、
// add_mail_contact（写通信录）、send_email（只建草稿）。
// 工具不发 SMTP、不把 confirmed 当本人授权；实际投递走用户确认命令。
package mailtool

// 工具线名：desktop hostToolClasses / OA 出网名单从此常量取，避免字符串漂移。
const (
	SearchContactsName  = "search_contacts"
	AddMailContactName  = "add_mail_contact"
	SendEmailName       = "send_email"
)
