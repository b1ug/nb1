"use strict";

(() => {
  const namespace = "http://www.w3.org/2000/svg";
  let gradientID = 0;

  function element(name, attributes = {}) {
    const node = document.createElementNS(namespace, name);
    for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, String(value));
    return node;
  }
  function add(svg, name, attributes) { svg.append(element(name, attributes)); }
  function path(svg, d, attributes = {}) { add(svg, "path", {d, ...attributes}); }

  const drawings = {
    set(svg) {
      add(svg, "circle", {cx: 14, cy: 14, r: 6, fill: "currentColor", stroke: "none"});
      path(svg, "M14 3v3 M14 22v3 M3 14h3 M22 14h3");
    },
    off(svg) {
      add(svg, "circle", {cx: 14, cy: 14, r: 8});
      path(svg, "M5 23L23 5");
    },
    fade(svg) {
      const id = "nb1-action-icon-fade-" + (++gradientID);
      const defs = element("defs"), gradient = element("linearGradient", {id, x1: "0%", y1: "0%", x2: "100%", y2: "0%"});
      gradient.append(element("stop", {offset: "0%", "stop-color": "currentColor", "stop-opacity": 0.08}), element("stop", {offset: "100%", "stop-color": "currentColor", "stop-opacity": 0.95}));
      defs.append(gradient); svg.append(defs);
      add(svg, "rect", {x: 3, y: 8, width: 22, height: 12, rx: 6, fill: "url(#" + id + ")"});
    },
    blink(svg) {
      path(svg, "M2 21H6V7H12V21H18V7H24V21H26");
    },
    breathe(svg) {
      add(svg, "circle", {cx: 14, cy: 14, r: 5});
      path(svg, "M8 6C11 3 17 3 20 6 M8 22C11 25 17 25 20 22");
      path(svg, "M7 14H2 M5 11L2 14L5 17 M21 14H26 M23 11L26 14L23 17");
    },
    pulse(svg) {
      path(svg, "M2 22H6C7.4 22 7.3 5 10 5C13 5 12 22 24 22H26");
    },
    alternate(svg) {
      path(svg, "M5 9H23 M19 5L23 9L19 13 M23 19H5 M9 15L5 19L9 23");
    },
    orbit(svg) {
      const planet = element("g", {transform: "rotate(-24 14 14)"});
      add(planet, "ellipse", {cx: 14, cy: 14, rx: 11.5, ry: 4.2});
      add(planet, "circle", {cx: 14, cy: 14, r: 7.2, fill: "#edc66a"});
      // The opaque globe hides the rear ring; a light edge separates the front.
      const front = "M2.5 14a11.5 4.2 0 0 0 23 0";
      path(planet, front, {stroke: "#fffbed", "stroke-width": 4.4});
      path(planet, front);
      svg.append(planet);
    },
    rainbow(svg) {
      svg.setAttribute("viewBox", "0 0 44 34");
      const colors = ["#ed4e63", "#f5922d", "#e9be21", "#20aa64", "#159fe2", "#5558cf", "#ae4fce"];
      colors.forEach((color, index) => {
        const radius = 20 - 3 * index;
        path(svg, "M" + (22 - radius) + " 29A" + radius + " " + radius + " 0 0 1 " + (22 + radius) + " 29", {stroke: color, "stroke-width": 1.7});
      });
    }
  };

  function create(kind) {
    const svg = element("svg", {viewBox: "0 0 28 28", width: 28, height: 28, fill: "none", stroke: "currentColor", "stroke-width": 2, "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true", focusable: "false", class: "action-icon"});
    if (typeof kind === "string" && Object.prototype.hasOwnProperty.call(drawings, kind)) drawings[kind](svg);
    else {
      add(svg, "circle", {cx: 14, cy: 14, r: 8});
      add(svg, "circle", {cx: 14, cy: 14, r: 2, fill: "currentColor", stroke: "none"});
    }
    return svg;
  }

  window.NB1ActionIcons = Object.freeze({create});
})();
