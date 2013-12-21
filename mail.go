package spamme

import (
	"appengine"
	"appengine/datastore"
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/mail"
	"time"
)

const (
	deleteAfter = 24 * time.Hour
	limit       = 100
	kind        = "Mail"

	// TODO: Add a button to immediately delete a message.
	// TODO: Add a button to pin a message to keep it around another 2h.
	// TODO: Pagination and search?
	mailHTML = `<html><body>
<h1>{{.To}}</h1>
{{if .Mails}}
<table border="1">
  {{range .Mails}}
    <tr>
      <td>{{.Received}}</td>
      <td>
        {{if .Raw}}<pre>{{.Raw}}</pre>
        {{else}}
          <h3>{{.Subj}}</h3>
          <pre>{{.Body}}</pre>
        {{end}}
      </td>
    </tr>
  {{end}}
</table>
{{else}}
  No mails have been sent to this address.
{{end}}
</body></html>`

	explainHTML = `<html><body>
  <h3>What is this?</h3>
  <p>Send an email to <b><i>anything</i>@spam-me.appspotmail.com</b>, then visit <a href="/inbox/anything">/inbox/anything</a> to see the emails it has received.</p>
  <p>This is useful for debugging sending email, and also for signing up for spammy services that require email account authentication.</p>
  <p>This service is *public* and *not at all secure or reliable*. Please don't use this for anything serious, ever. I mean it.</p>
</body></html>`
)

func init() {
	http.HandleFunc("/_ah/mail/", inbound)
	http.HandleFunc("/reap", reapMail)
	http.HandleFunc("/inbox/", view)
	http.HandleFunc("/", explain)
}

var mailTmpl = template.Must(template.New("mails").Parse(mailHTML))

type Mail struct {
	To          string
	Text        []byte
	Received    time.Time
	DeleteAfter time.Time
}

// inbound handles incoming email requests by persisting a new Mail and enqueing
// a task to delete it later.
func inbound(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		c.Errorf("reading mail: %v", err.Error())
		return
	}
	defer r.Body.Close()

	now := time.Now()
	m := Mail{
		To:          r.URL.Path[len("/_ah/mail/"):],
		Text:        b,
		Received:    now,
		DeleteAfter: now.Add(deleteAfter),
	}
	if _, err = datastore.Put(c, datastore.NewIncompleteKey(c, kind, nil), &m); err != nil {
		c.Errorf("saving mail: %v", err.Error())
	}
}

// reapMail runs periodically and deletes old mail.
func reapMail(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	q := datastore.NewQuery(kind).
		Filter("DeleteAfter >=", time.Now()).
		KeysOnly()
	ks, err := q.GetAll(c, nil)
	c.Infof("deleting %d mails", len(ks))
	if err != nil {
		c.Errorf("getting keys: %v", err)
		return
	}
	err = datastore.DeleteMulti(c, ks)
	if err != nil {
		c.Errorf("deleting: %v", err)
	}
}

func explain(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, explainHTML)
}

// view lists the Mails sent to a particular address.
func view(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	to := r.URL.Path[len("/inbox/"):] + "@spam-me.appspotmail.com"
	q := datastore.NewQuery(kind).
		Filter("To =", to).
		Order("-Received").
		Limit(limit)
	cnt, err := q.Count(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mails := make([]tmplData, 0, cnt)
	for t := q.Run(c); ; {
		var m Mail
		_, err := t.Next(&m)
		if err == datastore.Done {
			break
		} else if err != nil {
			c.Errorf("getting mail: %v", err)
			continue
		}

		td := tmplData{Received: m.Received.Format(time.RFC850)}
		parsed, err := mail.ReadMessage(bytes.NewReader(m.Text))
		if err != nil {
			c.Infof("error parsing message: %v", err)
			td.Raw = string(m.Text)
		} else {
			td.Subj = parsed.Header.Get("Subject")
			all, _ := ioutil.ReadAll(parsed.Body)
			td.Body = string(all)
		}
		mails = append(mails, td)
	}
	mailTmpl.Execute(w, struct {
		To    string
		Mails []tmplData
	}{to, mails})
}

type tmplData struct {
	Subj, Body, Received, Raw string
}
