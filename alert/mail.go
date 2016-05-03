package alert

import (
	"bytes"
	"github.com/boivie/lovebeat/config"
	"net/smtp"
	"strings"
	"text/template"
)

type mail struct {
	To      string
	Subject string
	Body    string
}

type mailAlerter struct {
	cfg *config.ConfigMail
}

const (
	TMPL_BODY = `The status for view '{{.View.Name}}' has changed from '{{.Previous | ToUpper}}' to '{{.Current | ToUpper}}'
`
	TMPL_SUBJECT = `[LOVEBEAT] {{.View.Name}}-{{.View.IncidentNbr}}`
	TMPL_EMAIL   = `From: {{.From}}
To: {{.To}}
Subject: {{.Subject}}
MIME-version: 1.0
Content-Type: text/html; charset="UTF-8"

{{.Message}}`
)

func renderTemplate(tmpl string, context map[string]interface{}) string {
	funcMap := template.FuncMap{
		"ToUpper": strings.ToUpper,
	}
	t, err := template.New("template").Funcs(funcMap).Parse(tmpl)
	if err != nil {
		log.Error("error trying to parse mail template: %s", err)
		return ""
	}
	var doc bytes.Buffer

	err = t.Execute(&doc, context)
	if err != nil {
		log.Error("Failed to render template: %s", err)
		return ""
	}
	return doc.String()
}

func createMail(address string, ev AlertInfo) mail {
	var context = make(map[string]interface{})
	context["View"] = ev.View
	context["Previous"] = ev.Previous
	context["Current"] = ev.Current

	var body = renderTemplate(TMPL_BODY, context)
	var subject = renderTemplate(TMPL_SUBJECT, context)
	return mail{
		To:      address,
		Subject: subject,
		Body:    body,
	}
}

func (m mailAlerter) Notify(cfg config.ConfigAlert, ev AlertInfo) {
	if cfg.Mail != "" {
		mail := createMail(cfg.Mail, ev)
		sendMail(m.cfg, mail)
	}
}

func sendMail(cfg *config.ConfigMail, mail mail) {
	log.Info("Sending from %s on host %s", cfg.From, cfg.Server)
	var context = make(map[string]interface{})
	context["From"] = cfg.From
	context["To"] = mail.To
	context["Subject"] = mail.Subject
	context["Message"] = mail.Body

	contents := renderTemplate(TMPL_EMAIL, context)
	var to = strings.Split(mail.To, ",")
	var err = smtp.SendMail(cfg.Server, nil, cfg.From, to,
		[]byte(contents))
	if err != nil {
		log.Error("Failed to send e-mail: %s", err)
	}
}

func NewMailAlerter(cfg config.Config) AlerterBackend {
	log.Debug("Sending mail via %s, from %s", cfg.Mail.Server, cfg.Mail.From)
	return &mailAlerter{&cfg.Mail}
}
