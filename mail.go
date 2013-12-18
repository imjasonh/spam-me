package spamme

import (
	"appengine"
	"appengine/datastore"
	"appengine/taskqueue"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"time"
)

const (
	delay = 2 * time.Hour
	limit = 100

	// TODO: Add a button to immediately delete a message.
	// TODO: Add a button to pin a message to keep it around another 2h.
	// TODO: Pagination and search?
	mailHTML = `<html><body>
<h3>Mails to: {{.To}}</h3>
{{if .Mails}}
<table border="1">
  {{range .Mails}}
    <tr>
      <td>{{.Received}}</td>
      <td><pre>{{.Text}}</pre></td>
    </tr>
  {{end}}
</table>
{{else}}
  No mails have been sent to this address.
{{end}}
</body></html>`

	explainHTML = `<html><body>
  <h3>What is this?</h3>
  <p>Send an email to <b><i>anything</i>@spam-me.appspotmail.com</b>, then visit <a href="/anything">/anything</a> to see the emails it has received.</p>
  <p>This is useful for debugging sending email, and also for signing up for spammy services that require email account authentication.</p>
  <p>This service is *public* and *not at all secure or reliable*. Please don't use this for anything serious, ever. I mean it.</p>
</body></html>`
)

func init() {
	http.HandleFunc("/_ah/mail/", inbound)
	http.HandleFunc("/_ah/queue/reapMail", reapMail)
	http.HandleFunc("/", view)
}

var mailTmpl = template.Must(template.New("mails").Parse(mailHTML))

type Mail struct {
	To       string
	Text     []byte
	Received time.Time
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

	m := Mail{
		To:       r.URL.Path[len("/_ah/mail/"):],
		Text:     b,
		Received: time.Now(),
	}
	dsKey, err := datastore.Put(c, datastore.NewIncompleteKey(c, "Mail", nil), &m)
	if err != nil {
		c.Errorf("saving mail: %v", err.Error())
		return
	}

	task := taskqueue.Task{
		Delay:   delay,
		Payload: []byte(dsKey.String()),
	}
	if _, err = taskqueue.Add(c, &task, "reapMail"); err != nil {
		c.Errorf("enqueing task: %v", err)
	}
}

// reapMail handles a TaskQueue request to delete an old Mail.
// TODO: Instead of enqueueing this when mails are received, run an hourly job
// that deletes anything received >2h ago.
func reapMail(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	key, err := ioutil.ReadAll(r.Body)
	if err != nil {
		c.Errorf("reading key: %v", err)
		return
	}
	dsKey := datastore.NewKey(c, "Mail", string(key), 0, nil)
	if err := datastore.Delete(c, dsKey); err != nil {
		c.Errorf("deleting mail: %v", err)
	}
}

// view lists the Mails sent to a particular address.
func view(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	if r.URL.Path == "/" {
		fmt.Fprintf(w, explainHTML)
		return
	}
	to := r.URL.Path[1:] + "@spam-me.appspotmail.com"

	q := datastore.NewQuery("Mail").
		Filter("To =", to).
		Order("-Received").
		Limit(limit)
	cnt, err := q.Count(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mails := make([]map[string]string, cnt)
	i := 0
	for t := q.Run(c); ; i++ {
		var m Mail
		_, err := t.Next(&m)
		if err == datastore.Done {
			break
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mails[i] = map[string]string{
			"Text":     string(m.Text),
			"Received": m.Received.String(),
		}
	}
	mailTmpl.Execute(w, map[string]interface{}{
		"To":    to,
		"Mails": mails,
	})
}
