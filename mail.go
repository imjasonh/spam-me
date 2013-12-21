package spamme

import (
	"appengine"
	"appengine/datastore"
	"bytes"
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
      <td>
        <form action="/pin" method="POST">
          <input type="hidden" name="key" value="{{.Key}}" />
          <input type="submit" value="Pin" />
        </form>
        <form action="/delete" method="POST">
          <input type="hidden" name="key" value="{{.Key}}" />
          <input type="submit" value="Delete Now" />
        </form>
      </td>
    </tr>
  {{end}}
</table>
{{else}}
  No mails have been received at this address.
{{end}}
</body></html>`
)

func init() {
	http.HandleFunc("/_ah/mail/", inbound)
	http.HandleFunc("/reap", reapMail)
	http.HandleFunc("/pin", pin)
	http.HandleFunc("/delete", delete2)
	http.HandleFunc("/inbox/", view)
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
		k, err := t.Next(&m)
		if err == datastore.Done {
			break
		} else if err != nil {
			c.Errorf("getting mail: %v", err)
			continue
		}

		td := tmplData{
			Key:      k.Encode(),
			Received: m.Received.Format(time.RFC850),
		}
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
	Key, Subj, Body, Received, Raw string
}

func pin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "must POST", http.StatusNotAcceptable)
		return
	}
	c := appengine.NewContext(r)
	s := r.FormValue("key")
	if s == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	k, err := datastore.DecodeKey(s)
	if err != nil {
		c.Errorf("decoding key: %v", err)
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	var m Mail
	err = datastore.Get(c, k, &m)
	if err == datastore.ErrNoSuchEntity {
		http.Error(w, "not found", http.StatusNotFound)
	} else if err != nil {
		c.Errorf("getting mail: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
	} else {
		m.DeleteAfter = time.Now().Add(deleteAfter)
		_, err = datastore.Put(c, k, &m)
		if err != nil {
			c.Errorf("saving mail: %v", err)
			http.Error(w, "error", http.StatusInternalServerError)
		}
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}

func delete2(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "must POST", http.StatusNotAcceptable)
		return
	}
	c := appengine.NewContext(r)
	s := r.FormValue("key")
	if s == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	k, err := datastore.DecodeKey(s)
	if err != nil {
		c.Errorf("decoding key: %v", err)
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	err = datastore.Delete(c, k)
	if err != nil {
		c.Errorf("deleting: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}
