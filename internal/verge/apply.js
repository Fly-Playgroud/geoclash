var __geoclash = __GEOCLASH_DATA__;

/**
 * 把 geoclash 的实测分组注入配置。先清除上一次注入的内容，因此重复执行结果不变（幂等）。
 *
 * 只使用 ES5 语法，兼容 Clash Verge Rev 的 boa 引擎和 geoclash 内置的 goja 引擎。
 *
 * @param config - Clash 配置对象，会被就地修改
 * @returns 修改后的配置
 */
function __geoclashApply(config) {
  var G = __geoclash;
  if (!config || typeof config !== "object") return config;

  var BUILTIN = { "DIRECT": 1, "REJECT": 1, "REJECT-DROP": 1, "PASS": 1, "PASS-RULE": 1, "COMPATIBLE": 1, "GLOBAL": 1 };
  var has = function (o, k) { return Object.prototype.hasOwnProperty.call(o, k); };
  var arr = function (v) { return Object.prototype.toString.call(v) === "[object Array]" ? v : []; };
  var own = function (n) { return typeof n === "string" && n.indexOf(G.prefix) === 0; };
  var i, j, k, g;

  // ---------- 1. 清除上一次注入 ----------
  var providers = config["proxy-providers"];
  if (!providers || typeof providers !== "object") providers = {};
  for (k in providers) {
    if (has(providers, k) && own(k)) delete providers[k];
  }

  var groups = arr(config["proxy-groups"]);
  var removed = {};
  removed[G.selector] = true;
  // 国家组：use 全部指向 geoclash 的 provider
  for (i = 0; i < groups.length; i++) {
    var use = arr(groups[i].use);
    if (!use.length) continue;
    var allOwn = true;
    for (j = 0; j < use.length; j++) if (!own(use[j])) allOwn = false;
    if (allOwn) removed[groups[i].name] = true;
  }
  // 旧的选择组（例如改过名）：没有 use、成员全部是被清除的国家组
  for (i = 0; i < groups.length; i++) {
    g = groups[i];
    var ps0 = arr(g.proxies);
    if (removed[g.name] || g.type !== "select" || !ps0.length || arr(g.use).length) continue;
    var allRemoved = true;
    for (j = 0; j < ps0.length; j++) if (!removed[ps0[j]]) allRemoved = false;
    if (allRemoved) removed[g.name] = true;
  }
  var kept = [];
  for (i = 0; i < groups.length; i++) {
    g = groups[i];
    if (!g || removed[g.name]) continue;
    if (g.proxies) {
      var ps = arr(g.proxies), left = [];
      for (j = 0; j < ps.length; j++) if (!removed[ps[j]]) left.push(ps[j]);
      g.proxies = left;
    }
    kept.push(g);
  }
  groups = kept;

  var processRules = [];
  for (i = 0; i < G.processes.length; i++) processRules.push("PROCESS-NAME," + G.processes[i] + ",DIRECT");
  var rules = [], oldRules = arr(config.rules);
  for (i = 0; i < oldRules.length; i++) {
    if (/^PROCESS-NAME,geoclash(\.exe)?,DIRECT$/.test(oldRules[i])) continue;
    if (processRules.indexOf(oldRules[i]) >= 0) continue;
    rules.push(oldRules[i]);
  }

  // ---------- 2. 注入 provider 与国家组 ----------
  var countryGroups = [], names = [];
  for (i = 0; i < G.groups.length; i++) {
    var x = G.groups[i], pname = G.prefix + x.id;
    providers[pname] = {
      type: "http",
      url: G.base + "/provider/" + encodeURIComponent(x.id),
      interval: G.providerInterval,
      path: G.pathDir + "/" + pname + ".yaml",
      proxy: "DIRECT",
      "health-check": { enable: true, url: x.url, interval: x.interval }
    };
    var cg = { name: x.name, type: x.type, use: [pname], url: x.url, interval: x.interval };
    if (x.type === "url-test") cg.tolerance = x.tolerance;
    countryGroups.push(cg);
    names.push(x.name);
  }

  config["proxy-providers"] = providers;
  config.rules = processRules.concat(rules);
  if (!countryGroups.length) {
    config["proxy-groups"] = groups;
    return config;
  }

  // ---------- 3. 挂接 ----------
  var byName = {};
  for (i = 0; i < groups.length; i++) byName[groups[i].name] = groups[i];
  var isGroup = function (n) { return has(byName, n); };
  var excludes = [];
  for (i = 0; i < G.attach.exclude.length; i++) {
    try { excludes.push(new RegExp(G.attach.exclude[i])); } catch (e) { console.error("geoclash: 无效的 attach-exclude 正则 " + G.attach.exclude[i]); }
  }
  var excluded = function (n) {
    for (var t = 0; t < excludes.length; t++) if (excludes[t].test(n)) return true;
    return false;
  };
  // 成员中直接包含真实节点（来自 proxies 或 proxy-providers）
  var hasRealMember = function (grp) {
    if (grp["include-all"] || grp["include-all-proxies"] || grp["include-all-providers"]) return true;
    if (arr(grp.use).length) return true;
    var m = arr(grp.proxies);
    for (var t = 0; t < m.length; t++) if (!isGroup(m[t]) && !has(BUILTIN, m[t])) return true;
    return false;
  };
  // 默认选择（第一个成员）逐层解析后是否指向真实节点
  var defaultIsReal = function (grp, seen) {
    if (seen[grp.name]) return false;
    seen[grp.name] = true;
    var m = arr(grp.proxies);
    if (!m.length) return hasRealMember(grp);
    if (has(BUILTIN, m[0])) return false;
    if (isGroup(m[0])) return defaultIsReal(byName[m[0]], seen);
    return true;
  };
  var listed = {};
  for (i = 0; i < G.attach.groups.length; i++) listed[G.attach.groups[i]] = true;

  // 先基于原始配置做完全部判断，再统一修改，结果与分组顺序无关
  var plan = [];
  for (i = 0; i < groups.length; i++) {
    g = groups[i];
    if (g.type !== "select") continue;
    var ok = false;
    if (G.attach.mode === "auto") ok = hasRealMember(g) && !excluded(g.name);
    else if (G.attach.mode === "all") ok = !excluded(g.name);
    else if (G.attach.mode === "list") ok = has(listed, g.name);
    if (ok) plan.push({ group: g, first: defaultIsReal(g, {}) });
  }
  for (i = 0; i < plan.length; i++) {
    var members = arr(plan[i].group.proxies).slice();
    if (plan[i].first) members.unshift(G.selector);
    else members.push(G.selector);
    plan[i].group.proxies = members;
  }

  // ---------- 4. 写回 ----------
  // 实测分组放在分组列表最前面：「🌍 实测国家」在首位，其后是各国家组，再后是原有分组
  var selector = { name: G.selector, type: "select", proxies: names };
  config["proxy-groups"] = [selector].concat(countryGroups, groups);
  return config;
}

try {
  main = (function (userMain) {
    return function (config, profileName) {
      var c = typeof userMain === "function" ? userMain(config, profileName) : config;
      return __geoclashApply(c);
    };
  })(typeof main === "function" ? main : undefined);
} catch (e) {
  console.error("geoclash: 无法包装 main（可能是 const 声明）: " + e);
}
