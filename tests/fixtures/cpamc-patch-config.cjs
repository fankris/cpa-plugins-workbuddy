// TEST ONLY: original CPAMC v1.25.3 patchConfig body; signature/export adapted.
// MIT notice: ../../licenses/CPAMC-MIT.txt. Not used by runtime.
module.exports = async function patchConfig(id, changes) {
    // Form fields are complete values: clearing removes a field and editing an
    // object replaces that object. A v8 PATCH would instead retain nulls and
    // recursively merge objects, so merge touched fields into the latest object
    // and replace only this plugin instance (never the complete config tree).
    const assertConnection = guardConfigConnection();
    const next = { ...(await pluginsApi.getConfig(id)) };
    assertConnection();
    for (const [key, value] of Object.entries(changes)) {
      if (value === null) delete next[key];
      else next[key] = value;
    }
    return apiClient.put(`/config/plugins/configs/${encodeURIComponent(id)}`, next);
};
