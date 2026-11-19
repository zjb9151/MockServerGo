package admin

import (
	"net/http"

	"csmock/internal/model"
)

func (s *Server) listInterfaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListInterfaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取接口失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": items})
}

func (s *Server) createInterface(w http.ResponseWriter, r *http.Request) {
	iface, err := decodeInterfaceRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	iface.ID = 0
	if err := s.store.CreateInterface(&iface); err != nil {
		writeErr(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, iface)
}

func (s *Server) interfaceByID(w http.ResponseWriter, r *http.Request, rest string) {
	id, extra, ok := parseID(rest)
	if !ok || (extra != "" && extra != "toggle" && extra != "rules") {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	if extra == "rules" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		if _, err := s.store.GetInterface(id); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		rules, err := s.store.ListRulesByInterface(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取规则失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
		return
	}
	if extra == "toggle" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		iface, err := s.store.ToggleInterface(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, iface)
		return
	}
	switch r.Method {
	case http.MethodGet:
		iface, err := s.store.GetInterface(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, iface)
	case http.MethodPut:
		iface, err := decodeInterfaceRequest(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		iface.ID = id
		old, err := s.store.GetInterface(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		iface.CreatedAt = old.CreatedAt
		if err := s.store.UpdateInterface(&iface); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, iface)
	case http.MethodDelete:
		if err := s.store.DeleteInterface(id); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func decodeInterfaceRequest(r *http.Request) (model.Interface, error) {
	body, err := readBody(r, 1<<20)
	if err != nil {
		return model.Interface{}, errString("读取请求失败")
	}
	iface, err := model.DecodeInterface(body)
	if err != nil {
		return model.Interface{}, errString("接口 JSON 无法解析")
	}
	if err := iface.Normalize(); err != nil {
		return model.Interface{}, err
	}
	return iface, nil
}
