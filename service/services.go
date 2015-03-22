package service

import (
	"github.com/boivie/lovebeat/backend"
	"github.com/boivie/lovebeat/metrics"
	"github.com/boivie/lovebeat/model"
	"github.com/op/go-logging"

	"regexp"
	"time"
)

type Services struct {
	be                   backend.Backend
	services             map[string]*Service
	views                map[string]*View
	upsertServiceCmdChan chan *upsertServiceCmd
	deleteServiceCmdChan chan string
	getServicesChan      chan *getServicesCmd
	getServiceChan       chan *getServiceCmd
	getViewsChan         chan *getViewsCmd
	getViewChan          chan *getViewCmd
}

const (
	MAX_UNPROCESSED_PACKETS = 1000
	EXPIRY_INTERVAL         = 1
)

var (
	log      = logging.MustGetLogger("lovebeat")
	counters = metrics.NopMetrics()
	StateMap = map[string]int{
		model.STATE_PAUSED:  0,
		model.STATE_OK:      1,
		model.STATE_WARNING: 2,
		model.STATE_ERROR:   3,
	}
)

func (svcs *Services) updateViews(ts int64, serviceName string) {
	for _, view := range svcs.views {
		if view.contains(serviceName) {
			var ref = *view
			view.update(ts)
			if view.data.State != ref.data.State {
				view.save(svcs.be, &ref, ts)
				if view.hasAlert(&ref) {
					// TODO: Send alert
				}
			}
		}
	}
}

func (svcs *Services) getService(name string) *Service {
	var s, ok = svcs.services[name]
	if !ok {
		log.Debug("Asked for unknown service %s", name)
		s = newService(name)
		svcs.services[name] = s
	}
	return s
}

func (svcs *Services) getView(name string) *View {
	var s, ok = svcs.views[name]
	if !ok {
		log.Debug("Asked for unknown view %s", name)
		s = newView(svcs.services, name)
		svcs.views[name] = s
	}
	return s
}

func (svcs *Services) Monitor() {
	period := time.Duration(EXPIRY_INTERVAL) * time.Second
	ticker := time.NewTicker(period)
	svcs.reload()
	for {
		select {
		case <-ticker.C:
			var ts = now()
			for _, s := range svcs.services {
				if s.data.State == model.STATE_PAUSED ||
					s.data.State == s.stateAt(ts) {
					continue
				}
				var ref = *s
				s.update(ts)
				s.save(svcs.be, &ref, ts)
				svcs.updateViews(ts, s.name())
			}
		case c := <-svcs.getServicesChan:
			var ret []model.Service
			var view, ok = svcs.views[c.View]
			if ok {
				for _, s := range svcs.services {
					if view.contains(s.name()) {
						ret = append(ret, s.data)
					}
				}
			}
			c.Reply <- ret
		case c := <-svcs.getServiceChan:
			var ret = svcs.services[c.Name]
			if ret == nil {
				c.Reply <- nil
			} else {
				c.Reply <- &ret.data
			}
		case c := <-svcs.getViewsChan:
			var ret []model.View
			for _, v := range svcs.views {
				ret = append(ret, v.data)
			}
			c.Reply <- ret
		case c := <-svcs.getViewChan:
			var ret = svcs.views[c.Name]
			if ret == nil {
				c.Reply <- nil
			} else {
				c.Reply <- &ret.data
			}
		case c := <-svcs.deleteServiceCmdChan:
			log.Info("SERVICE '%s', deleted", c)
			var ts = now()
			var s = svcs.getService(c)
			delete(svcs.services, s.name())
			svcs.be.DeleteService(s.name())
			svcs.updateViews(ts, s.name())
		case c := <-svcs.upsertServiceCmdChan:
			var ts = now()
			var s = svcs.getService(c.Service)
			var ref = *s

			if c.RegisterBeat {
				s.registerBeat(ts)
			}

			// Don't re-calculate 'auto' if we already have values
			if c.WarningTimeout == TIMEOUT_AUTO &&
				s.data.WarningTimeout == -1 {
				s.data.WarningTimeout = TIMEOUT_AUTO
				s.data.PreviousBeats = make([]int64, model.PREVIOUS_BEATS_COUNT)
			} else if c.WarningTimeout == TIMEOUT_CLEAR {
				s.data.WarningTimeout = TIMEOUT_CLEAR
			} else if c.WarningTimeout > 0 {
				s.data.WarningTimeout = c.WarningTimeout
			}
			if c.ErrorTimeout == TIMEOUT_AUTO &&
				s.data.ErrorTimeout == -1 {
				s.data.ErrorTimeout = TIMEOUT_AUTO
				s.data.PreviousBeats = make([]int64, model.PREVIOUS_BEATS_COUNT)
			} else if c.ErrorTimeout == TIMEOUT_CLEAR {
				s.data.ErrorTimeout = TIMEOUT_CLEAR
			} else if c.ErrorTimeout > 0 {
				s.data.ErrorTimeout = c.ErrorTimeout
			}
			s.update(ts)
			s.save(svcs.be, &ref, ts)
			svcs.updateViews(ts, s.name())
		}
	}
}

func (svcs *Services) createAllView() *View {
	var ree, _ = regexp.Compile("")
	return &View{
		services: svcs.services,
		data: model.View{
			Name: "all",
		},
		ree: ree,
	}
}

func (svcs *Services) reload() {
	svcs.services = make(map[string]*Service)
	svcs.views = make(map[string]*View)

	for _, s := range svcs.be.LoadServices() {
		var svc = &Service{data: *s}
		if svc.data.PreviousBeats == nil ||
			len(svc.data.PreviousBeats) != model.PREVIOUS_BEATS_COUNT {
			svc.data.PreviousBeats = make([]int64, model.PREVIOUS_BEATS_COUNT)
		}
		svcs.services[s.Name] = svc
	}

	svcs.views["all"] = svcs.createAllView()

	for _, v := range svcs.be.LoadViews() {
		var ree, _ = regexp.Compile(v.Regexp)
		svcs.views[v.Name] = &View{
			services: svcs.services,
			data:     *v,
			ree:      ree,
		}
	}
}

func NewServices(beiface backend.Backend, m metrics.Metrics) *Services {
	counters = m
	svcs := new(Services)
	svcs.be = beiface
	svcs.deleteServiceCmdChan = make(chan string, 5)
	svcs.upsertServiceCmdChan = make(chan *upsertServiceCmd, MAX_UNPROCESSED_PACKETS)
	svcs.getServicesChan = make(chan *getServicesCmd, 5)
	svcs.getServiceChan = make(chan *getServiceCmd, 5)
	svcs.getViewsChan = make(chan *getViewsCmd, 5)
	svcs.getViewChan = make(chan *getViewCmd, 5)

	return svcs
}
