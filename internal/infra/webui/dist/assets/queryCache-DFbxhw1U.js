const c=new Map;function a(t){return c.get(t)}function n(t,e){c.set(t,e)}function s(t){for(const e of[...c.keys()])(e===t||e.startsWith(t))&&c.delete(e)}export{n as a,s as b,a as c};
