'use strict';
'require view';
'require rpc';
'require ui';

// Inject stylesheet once
(function() {
	var id = 'vpnsub-css';
	if (!document.getElementById(id)) {
		var link = document.createElement('link');
		link.id   = id;
		link.rel  = 'stylesheet';
		link.href = L.resource('vpnsub/vpnsub.css');
		document.head.appendChild(link);
	}
}());

// No `expect` on any call — handle raw response objects everywhere
var callGetConfig = rpc.declare({ object: 'vpnsub', method: 'get_config' });
var callSetConfig = rpc.declare({ object: 'vpnsub', method: 'set_config', params: ['config'] });
var callRunScript = rpc.declare({ object: 'vpnsub', method: 'run_script', params: ['dry_run', 'verbose'] });
var callGetLog    = rpc.declare({ object: 'vpnsub', method: 'get_log' });
var callGetSyslog  = rpc.declare({ object: 'vpnsub', method: 'get_syslog',    params: ['lines'] });
var callTestUrl    = rpc.declare({ object: 'vpnsub', method: 'test_url',      params: ['url'] });
var callGetSbStatus = rpc.declare({ object: 'vpnsub', method: 'get_sb_status' });

// ── DynamicList (with List ↔ Textarea toggle) ─────────────────────────────────
function dynList(values, placeholder) {
	var mode = 'list'; // 'list' | 'textarea'

	// ── list mode ────────────────────────────────────────────────────────────
	var listEl = E('div', { 'class': 'vpnsub-dynlist' });

	function makeRow(val) {
		var input = E('input', {
			'type': 'text',
			'class': 'cbi-input-text',
			'value': val || '',
			'placeholder': placeholder || ''
		});
		var remove = E('button', {
			'type': 'button',
			'class': 'vpnsub-dynlist-remove cbi-button',
			'click': function() { listEl.removeChild(row); }
		}, '✕');
		var row = E('div', { 'class': 'vpnsub-dynlist-row' }, [input, remove]);
		return row;
	}

	var addBtn = E('button', {
		'type': 'button',
		'class': 'vpnsub-dynlist-add cbi-button',
		'click': function() {
			var r = makeRow('');
			listEl.insertBefore(r, addBtn);
			r.querySelector('input').focus();
		}
	}, '+');

	function initList(vals) {
		// Remove all rows, keep addBtn
		while (listEl.firstChild) listEl.removeChild(listEl.firstChild);
		(vals || []).forEach(function(v) { listEl.appendChild(makeRow(v)); });
		listEl.appendChild(addBtn);
	}

	function getListValues() {
		return Array.prototype.slice.call(
			listEl.querySelectorAll('.vpnsub-dynlist-row input')
		).map(function(el) { return el.value.trim(); })
		 .filter(function(v) { return v !== ''; });
	}

	// ── textarea mode ────────────────────────────────────────────────────────
	var taEl = E('textarea', {
		'class': 'cbi-input-textarea vpnsub-dynlist-textarea',
		'rows': '6',
		'placeholder': placeholder ? placeholder + '\n' + placeholder : '',
		'style': 'display:none'
	});

	function getTextareaValues() {
		return taEl.value.split('\n')
			.map(function(v) { return v.trim(); })
			.filter(function(v) { return v !== ''; });
	}

	// ── toggle button ────────────────────────────────────────────────────────
	var toggleBtn = E('button', {
		'type': 'button',
		'class': 'vpnsub-dynlist-toggle cbi-button',
		'click': function() {
			if (mode === 'list') {
				// list → textarea
				taEl.value = getListValues().join('\n');
				listEl.style.display  = 'none';
				taEl.style.display    = '';
				toggleBtn.textContent = _('List mode');
				mode = 'textarea';
			} else {
				// textarea → list
				initList(getTextareaValues());
				taEl.style.display    = 'none';
				listEl.style.display  = '';
				toggleBtn.textContent = _('Text mode');
				mode = 'list';
			}
		}
	}, _('Text mode'));

	// ── assemble ─────────────────────────────────────────────────────────────
	initList(values);

	var wrap = E('div', { 'class': 'vpnsub-dynlist-wrap' }, [
		E('div', { 'class': 'vpnsub-dynlist-toolbar' }, [toggleBtn]),
		listEl,
		taEl
	]);

	return {
		node: wrap,
		getValue: function() {
			return mode === 'list' ? getListValues() : getTextareaValues();
		}
	};
}

// ── Layout helpers ────────────────────────────────────────────────────────────
function formRow(label, field, description) {
	var nodes = [
		E('label', { 'class': 'cbi-value-title' }, label),
		E('div', { 'class': 'cbi-value-field' }, Array.isArray(field) ? field : [field])
	];
	if (description)
		nodes.push(E('div', { 'class': 'cbi-value-description' }, description));
	return E('div', { 'class': 'cbi-value' }, nodes);
}

function setError(input, msg) {
	input.classList.add('vpnsub-invalid');
	var errEl = input.parentNode.querySelector('.vpnsub-errmsg');
	if (!errEl) {
		errEl = E('div', { 'class': 'vpnsub-errmsg' });
		input.parentNode.appendChild(errEl);
	}
	errEl.textContent = msg;
}

function clearError(input) {
	input.classList.remove('vpnsub-invalid');
	var errEl = input.parentNode.querySelector('.vpnsub-errmsg');
	if (errEl) errEl.parentNode.removeChild(errEl);
}

function isValidUrl(s) {
	return /^https?:\/\/.+/.test(s);
}

// ── Collapsible dynList row ───────────────────────────────────────────────────
function makeCollapsible(label, widget, description) {
	var badge  = E('span', { 'class': 'vpnsub-count-badge' });
	var arrow  = E('span', { 'class': 'vpnsub-collapse-arrow' }, '▶');
	var body   = E('div',  { 'class': 'vpnsub-collapse-body', 'style': 'display:none' }, [
		widget.node
	]);
	if (description) {
		body.appendChild(E('div', { 'class': 'vpnsub-collapse-description' }, description));
	}

	function updateBadge() {
		var n = widget.getValue().length;
		badge.textContent  = n > 0 ? String(n) : '0';
		badge.style.opacity = n > 0 ? '1' : '0.4';
	}

	updateBadge();

	var toggle = E('div', {
		'class': 'vpnsub-collapse-toggle',
		'click': function() {
			var open = body.style.display !== 'none';
			body.style.display  = open ? 'none' : '';
			arrow.textContent   = open ? '▶' : '▼';
			badge.style.display = open ? '' : 'none';
			if (open) updateBadge();
		}
	}, [arrow, label, ' ', badge]);

	return E('div', { 'class': 'cbi-value vpnsub-routing-row' }, [toggle, body]);
}

// ── Tab switcher ──────────────────────────────────────────────────────────────
function makeTabs(tabs) {
	// tabs = [{id, label, content}]
	var btns = tabs.map(function(t, i) {
		return E('button', {
			'type': 'button',
			'class': 'vpnsub-tab' + (i === 0 ? ' vpnsub-tab-active' : ''),
			'data-tab': t.id,
			'click': function() {
				btns.forEach(function(b) { b.classList.remove('vpnsub-tab-active'); });
				this.classList.add('vpnsub-tab-active');
				tabs.forEach(function(tab) {
					tab.content.style.display = (tab.id === t.id) ? '' : 'none';
				});
			}
		}, t.label);
	});

	tabs.forEach(function(t, i) {
		t.content.style.display = i === 0 ? '' : 'none';
	});

	var tabBar  = E('div', { 'class': 'vpnsub-tabbar' }, btns);
	var wrapper = E('div', { 'class': 'vpnsub-tabs' }, [tabBar]);
	tabs.forEach(function(t) { wrapper.appendChild(t.content); });
	return wrapper;
}

// ── Main view ─────────────────────────────────────────────────────────────────
return view.extend({
	_widgets:        null,
	_subIdx:         0,
	_pollTimer:      null,
	_syslogTimer:    null,

	load: function() {
		return Promise.all([ callGetConfig(), callGetLog(), callGetSbStatus() ]);
	},

	render: function(results) {
		var self    = this;
		var data    = results[0];
		var logData = results[1];
		var sbData  = results[2];

		var cfg = (data && data.config)
		        ? data.config
		        : { log_level: 'warn', subscriptions: [] };

		// Proxies map from sing-box REST API (may be empty if API is down)
		var proxies  = (sbData && sbData.proxies)   ? sbData.proxies   : {};
		var tagNames = (sbData && sbData.tag_names) ? sbData.tag_names : {};

		self._widgets = {};
		self._subIdx  = (cfg.subscriptions || []).length;

		// ── Tab 1: Settings ───────────────────────────────────────────────────
		var logLevelSel = E('select', { 'class': 'cbi-input-select', 'id': 'vpnsub-log-level' });
		['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'].forEach(function(l) {
			var o = E('option', { value: l }, l);
			if (l === (cfg.log_level || 'warn')) o.selected = true;
			logLevelSel.appendChild(o);
		});

		var testUrlSettingInput = E('input', {
			'type': 'text',
			'class': 'cbi-input-text',
			'id': 'vpnsub-test-url-setting',
			'value': cfg.test_url || 'https://www.gstatic.com/generate_204',
			'placeholder': 'https://www.gstatic.com/generate_204'
		});

		var globalSection = E('div', { 'class': 'cbi-section' }, [
			E('legend', {}, _('Global settings')),
			formRow(_('Log level'), logLevelSel, _('Logging verbosity for sing-box')),
			formRow(_('URL test'), testUrlSettingInput,
				_('URL used by urltest groups to measure latency'))
		]);

		var subList = E('div', { 'id': 'vpnsub-sub-list' });
		(cfg.subscriptions || []).forEach(function(sub, i) {
			subList.appendChild(self._makeCard(sub, i, proxies, tagNames));
		});

		var addBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button cbi-button-add',
			'click': function() {
				var i    = self._subIdx++;
				var card = self._makeCard({ name: '', url: '', default: false }, i);
				subList.appendChild(card);
				card.scrollIntoView({ behavior: 'smooth' });
			}
		}, '+ ' + _('Add subscription'));

		var saveBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button cbi-button-save',
			'click': function() { self.handleSave(); }
		}, _('Save'));

		var settingsPane = E('div', { 'class': 'vpnsub-tab-pane' }, [
			globalSection,
			E('div', { 'class': 'cbi-section' }, [
				E('legend', {}, _('Subscriptions')),
				subList,
				E('div', { 'class': 'cbi-section-create' }, [addBtn])
			]),
			E('div', { 'class': 'cbi-page-actions' }, [saveBtn])
		]);

		// ── Tab 2: Logs ───────────────────────────────────────────────────────
		var logPre = E('pre', { 'id': 'vpnsub-log', 'class': 'vpnsub-log' });

		var initialLog = logData && logData.log ? logData.log : '';
		logPre.textContent = initialLog || _('No log yet. Run the script to see output.');

		var dryRunChk = E('input', { 'type': 'checkbox', 'id': 'vpnsub-dry-run' });
		var debugChk  = E('input', {
			'type': 'checkbox',
			'id': 'vpnsub-debug',
			// debug (-vvv) and dry-run are complementary but compatible
		});

		var runBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button cbi-button-action',
			'id': 'vpnsub-run-btn',
			'click': function() { self.handleRun(runBtn, logPre); }
		}, _('Run script'));

		var clearBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button',
			'click': function() { logPre.textContent = ''; }
		}, _('Clear'));

		var logsPane = E('div', { 'class': 'vpnsub-tab-pane' }, [
			E('div', { 'class': 'cbi-section' }, [
				E('legend', {}, _('Script output')),
				E('div', { 'class': 'vpnsub-run-options' }, [
					E('label', { 'class': 'vpnsub-run-option' }, [
						dryRunChk, '\u00a0', _('Dry run')
					]),
					E('label', { 'class': 'vpnsub-run-option' }, [
						debugChk, '\u00a0', _('Debug (-vvv)')
					])
				]),
				E('div', { 'class': 'vpnsub-log-actions' }, [runBtn, '\u00a0', clearBtn]),
				logPre
			])
		]);

		// ── Tab 3: sing-box syslog ────────────────────────────────────────────
		var syslogPre = E('pre', { 'id': 'vpnsub-syslog', 'class': 'vpnsub-log' },
			_('Loading…'));

		var syslogAutoChk = E('input', {
			'type': 'checkbox',
			'id': 'vpnsub-syslog-auto',
			'change': function() {
				if (this.checked) {
					self._startSyslogPoll(syslogPre);
				} else {
					self._stopSyslogPoll();
				}
			}
		});

		var syslogRefreshBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button',
			'click': function() { self._fetchSyslog(syslogPre); }
		}, _('Refresh'));

		var syslogPane = E('div', { 'class': 'vpnsub-tab-pane' }, [
			E('div', { 'class': 'cbi-section' }, [
				E('legend', {}, _('sing-box system log')),
				E('div', { 'class': 'vpnsub-run-options' }, [
					E('label', { 'class': 'vpnsub-run-option' }, [
						syslogAutoChk, '\u00a0', _('Auto-refresh (5s)')
					]),
					syslogRefreshBtn
				]),
				syslogPre
			])
		]);

		// Initial syslog load when tab is first rendered
		self._fetchSyslog(syslogPre);

		// ── Tab 4: Test ───────────────────────────────────────────────────────
		var testUrlInput = E('input', {
			'type': 'text',
			'class': 'cbi-input-text vpnsub-test-urlinput',
			'id': 'vpnsub-test-url',
			'placeholder': 'https://example.com',
			'keydown': function(ev) {
				if (ev.key === 'Enter') testBtn.click();
			}
		});

		var testBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button cbi-button-action',
			'click': function() { self.handleTest(testBtn, testResultsEl); }
		}, _('Test'));

		var testResultsEl = E('div', { 'id': 'vpnsub-test-results' });

		var testPane = E('div', { 'class': 'vpnsub-tab-pane' }, [
			E('div', { 'class': 'cbi-section' }, [
				E('legend', {}, _('VPN connectivity test')),
				E('div', { 'class': 'vpnsub-test-row-input' }, [testUrlInput, '\u00a0', testBtn]),
				E('div', { 'class': 'cbi-value-description' }, [
					_('Sends a request through tun0 and reports outgoing IP, geo-location, and which outbound handled it.')
				]),
				testResultsEl
			])
		]);

		// ── Assemble tabs ─────────────────────────────────────────────────────
		return E('div', { 'class': 'cbi-map' }, [
			E('h2', {}, _('VPN Subscriptions')),
			makeTabs([
				{ id: 'settings', label: _('Subscriptions'), content: settingsPane },
				{ id: 'logs',     label: _('Run & Output'),  content: logsPane },
				{ id: 'syslog',   label: _('Logs'),          content: syslogPane },
				{ id: 'test',     label: _('Test'),          content: testPane }
			])
		]);
	},

	// ── Render one subscription card ──────────────────────────────────────────
	_makeCard: function(sub, idx, proxies, tagNames) {
		var self   = this;
		var cardId = 'vpnsub-sub-' + idx;

		var domainsW = dynList(sub.domains || [], _('example.com'));
		var ipW      = dynList(sub.ip      || [], '192.168.0.0/16');
		var excludeW = dynList(sub.exclude || [], _('Russia'));
		self._widgets[idx] = { domains: domainsW, ip: ipW, exclude: excludeW };

		var nameInput = E('input', {
			'type': 'text',
			'class': 'cbi-input-text vpnsub-sub-name',
			'value': sub.name || '',
			'placeholder': _('Subscription name'),
			'input': function() { clearError(this); }
		});

		var urlInput = E('input', {
			'type': 'text',
			'class': 'cbi-input-text vpnsub-sub-url',
			'value': sub.url || '',
			'placeholder': 'https://',
			'input': function() { clearError(this); }
		});

		var defInput = E('input', {
			'type': 'radio',
			'name': 'vpnsub-default',
			'class': 'vpnsub-sub-default',
			'value': String(idx),
			'change': function() { self._updateDomainVisibility(); }
		});
		if (sub.default === true) defInput.checked = true;

		var removeBtn = E('button', {
			'type': 'button',
			'class': 'cbi-button cbi-button-negative vpnsub-sub-remove',
			'click': function() {
				var card = document.getElementById(cardId);
				if (card) card.parentNode.removeChild(card);
				delete self._widgets[idx];
			}
		}, _('Remove'));

		// sing-box status for this subscription's urltest group
		var groupKey   = sub.name ? sub.name + '-best' : null;
		var groupProxy = (proxies && groupKey) ? proxies[groupKey] : null;
		var statusEl   = null;
		if (groupProxy && groupProxy.now) {
			var nowTag  = groupProxy.now;
			var nowName = (tagNames && tagNames[nowTag]) ? tagNames[nowTag] : null;
			var label   = nowName ? nowTag + ' (' + nowName + ')' : nowTag;
			statusEl = E('span', { 'class': 'vpnsub-sub-status' }, '● ' + label);
		}

		var domainsRow = makeCollapsible(
			_('Domains'), domainsW,
			_('Traffic to these domains will be routed through this subscription'));
		if (sub.default === true) domainsRow.style.display = 'none';

		var ipRow = makeCollapsible(
			_('IP / CIDR'), ipW,
			_('Traffic to these IP ranges will be routed through this subscription'));
		if (sub.default === true) ipRow.style.display = 'none';

		return E('div', {
			'class': 'cbi-section-node vpnsub-sub-card',
			'id': cardId,
			'data-idx': String(idx)
		}, [
			E('div', { 'class': 'vpnsub-sub-header' }, [
				E('div', { 'class': 'vpnsub-sub-header-left' }, [
					E('strong', { 'class': 'vpnsub-sub-title' }, _('Subscription')),
					statusEl ? statusEl : ''
				]),
				removeBtn
			]),
			formRow(_('Name'), nameInput),
			formRow(_('URL'),  urlInput),
			formRow(_('Default'), E('label', {}, [
				defInput,
				'\u00a0' + _('Use as default outbound (fallback route)')
			]), _('Exactly one subscription must be marked as default')),
			domainsRow,
			ipRow,
			formRow(_('Exclude filters'), excludeW.node,
				_('Servers whose names contain any of these strings will be skipped'))
		]);
	},

	// ── Collect + validate ────────────────────────────────────────────────────
	_collectConfig: function() {
		var self    = this;
		var level   = document.getElementById('vpnsub-log-level').value;
		var testUrl = document.getElementById('vpnsub-test-url-setting').value.trim()
		            || 'https://www.gstatic.com/generate_204';
		var subs    = [];

		Array.prototype.forEach.call(
			document.querySelectorAll('.vpnsub-sub-card'),
			function(card) {
				var idx     = parseInt(card.getAttribute('data-idx'));
				var w       = self._widgets[idx] || {};
				var name    = card.querySelector('.vpnsub-sub-name').value.trim();
				var url     = card.querySelector('.vpnsub-sub-url').value.trim();
				var isDef   = card.querySelector('.vpnsub-sub-default').checked;
				var domains = w.domains ? w.domains.getValue() : [];
				var ip      = w.ip      ? w.ip.getValue()      : [];
				var exclude = w.exclude ? w.exclude.getValue() : [];

				var sub = { name: name, url: url };
				if (isDef)          sub.default = true;
				if (domains.length) sub.domains = domains;
				if (ip.length)      sub.ip      = ip;
				if (exclude.length) sub.exclude = exclude;
				subs.push(sub);
			}
		);

		return { log_level: level, test_url: testUrl, subscriptions: subs };
	},

	_validate: function() {
		var valid = true;

		Array.prototype.forEach.call(
			document.querySelectorAll('.vpnsub-sub-card'),
			function(card) {
				var nameEl = card.querySelector('.vpnsub-sub-name');
				var urlEl  = card.querySelector('.vpnsub-sub-url');
				var name   = nameEl.value.trim();
				var url    = urlEl.value.trim();

				clearError(nameEl);
				clearError(urlEl);

				if (!name) {
					setError(nameEl, _('Name is required'));
					valid = false;
				}
				if (!url) {
					setError(urlEl, _('URL is required'));
					valid = false;
				} else if (!isValidUrl(url)) {
					setError(urlEl, _('Enter a valid URL (https://...)'));
					valid = false;
				}
			}
		);

		if (!valid) return false;

		var cfg      = this._collectConfig();
		var defaults = cfg.subscriptions.filter(function(s) { return s.default === true; });

		if (cfg.subscriptions.length > 0 && defaults.length === 0) {
			ui.addNotification(null, E('p', _('Please select a default subscription')), 'warning');
			return false;
		}
		if (defaults.length > 1) {
			ui.addNotification(null, E('p', _('Only one subscription can be set as default')), 'warning');
			return false;
		}

		return cfg;
	},

	// ── Show/hide routing rows (Domains + IP) based on which sub is default ──
	_updateDomainVisibility: function() {
		Array.prototype.forEach.call(
			document.querySelectorAll('.vpnsub-sub-card'),
			function(card) {
				var radio     = card.querySelector('.vpnsub-sub-default');
				var isDefault = radio && radio.checked;
				Array.prototype.forEach.call(
					card.querySelectorAll('.vpnsub-routing-row'),
					function(row) { row.style.display = isDefault ? 'none' : ''; }
				);
			}
		);
	},

	// ── Save ──────────────────────────────────────────────────────────────────
	handleSave: function() {
		var cfg = this._validate();
		if (!cfg) return;

		return callSetConfig(cfg).then(function() {
			window.scrollTo({ top: 0, behavior: 'smooth' });
			ui.addNotification(null, E('p', _('Settings saved')), 'info');
		}).catch(function(err) {
			ui.addNotification(null, E('p', _('Save error: ') + (err.message || String(err))), 'error');
		});
	},

	// ── Test ──────────────────────────────────────────────────────────────────
	handleTest: function(btn, resultsEl) {
		var url = document.getElementById('vpnsub-test-url').value.trim();
		if (!url) return;

		// Clear and show spinner
		while (resultsEl.firstChild) resultsEl.removeChild(resultsEl.firstChild);
		resultsEl.appendChild(E('div', { 'class': 'vpnsub-test-spinner' }, _('Testing… (up to 30s)')));
		if (btn) btn.disabled = true;

		callTestUrl(url).then(function(res) {
			while (resultsEl.firstChild) resultsEl.removeChild(resultsEl.firstChild);

			if (!res || res.error) {
				resultsEl.appendChild(E('p', { 'class': 'vpnsub-test-error' },
					_('Error: ') + (res && res.error || _('no response from backend'))));
				return;
			}

			var rows = [
				[_('Domain'),     res.domain || '—'],
				[_('VPN status'), res.vpn_ok
					? '✅ ' + _('Connected') + (res.http_code ? ' (HTTP\u00a0' + res.http_code + ')' : '')
					: '❌ ' + _('Failed')   + (res.http_code ? ' (HTTP\u00a0' + res.http_code + ')' : '')],
				[_('Outbound'),   res.outbound  || '—']
			];

			var table = E('div', { 'class': 'vpnsub-test-table' },
				rows.map(function(r) {
					return E('div', { 'class': 'vpnsub-test-kv' }, [
						E('span', { 'class': 'vpnsub-test-key'   }, r[0]),
						E('span', { 'class': 'vpnsub-test-val'   }, r[1])
					]);
				})
			);
			resultsEl.appendChild(table);

		}).catch(function(err) {
			while (resultsEl.firstChild) resultsEl.removeChild(resultsEl.firstChild);
			resultsEl.appendChild(E('p', { 'class': 'vpnsub-test-error' },
				_('Error: ') + (err.message || String(err))));
		}).then(function() {
			if (btn) btn.disabled = false;
		});
	},

	// ── Syslog ────────────────────────────────────────────────────────────────
	_fetchSyslog: function(pre) {
		callGetSyslog(200).then(function(res) {
			if (!res) return;
			var text = res.log != null ? res.log : '';
			pre.textContent = text || _('(no sing-box entries in syslog)');
			pre.scrollTop   = pre.scrollHeight;
		});
	},

	_startSyslogPoll: function(pre) {
		var self = this;
		self._stopSyslogPoll();
		self._syslogTimer = setInterval(function() {
			self._fetchSyslog(pre);
		}, 5000);
	},

	_stopSyslogPoll: function() {
		if (this._syslogTimer) {
			clearInterval(this._syslogTimer);
			this._syslogTimer = null;
		}
	},

	// ── Run script + poll log ─────────────────────────────────────────────────
	handleRun: function(btn, logPre) {
		var self    = this;
		var dryRun  = document.getElementById('vpnsub-dry-run').checked;
		var verbose = document.getElementById('vpnsub-debug').checked ? 3 : 2;

		if (self._pollTimer) {
			clearInterval(self._pollTimer);
			self._pollTimer = null;
		}

		var modeLabel = dryRun ? ' [dry-run]' : '';
		logPre.textContent = _('Starting…') + modeLabel + '\n';
		if (btn) btn.disabled = true;

		callRunScript(dryRun, verbose).then(function() {
			self._pollTimer = setInterval(function() {
				callGetLog().then(function(res) {
					if (!res) return;

					var logText = res.log != null ? res.log : '';
					logPre.textContent = logText || _('(waiting for output…)');
					logPre.scrollTop   = logPre.scrollHeight;

					if (!res.running) {
						clearInterval(self._pollTimer);
						self._pollTimer = null;
						if (btn) btn.disabled = false;
					}
				});
			}, 2000);
		}).catch(function(err) {
			logPre.textContent += '\n' + _('Error: ') + (err.message || String(err));
			if (btn) btn.disabled = false;
		});
	}
});
