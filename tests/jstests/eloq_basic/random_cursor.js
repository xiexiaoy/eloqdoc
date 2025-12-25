// random_cursor.js
// Verify $sample uses Random Cursor fast path (MongoDB 4.0 compatible)

(function () {
    'use strict';

    var coll = db.random_cursor_test;
    coll.drop();

    var nDocs = 50000;
    var sampleSize = 50;

    print("Inserting documents: " + nDocs);

    var bulk = coll.initializeUnorderedBulkOp();
    for (var i = 0; i < nDocs; i++) {
        bulk.insert({ _id: i, x: i });
    }
    assert.writeOK(bulk.execute());

    print("Documents inserted.");

    var explain = coll.explain("executionStats").aggregate([
        { $sample: { size: sampleSize } }
    ]);

    printjson(explain);

    // ---------------- helpers ----------------

    function findStageInPipeline(stages, predicate) {
        for (var i = 0; i < stages.length; i++) {
            if (predicate(stages[i])) {
                return stages[i];
            }
        }
        return null;
    }

    // ---------------- assertions ----------------

    assert(explain.stages && explain.stages.length > 0,
        "Explain output has no stages");

    // 1. Must use Random Cursor
    var randomSampleStage = findStageInPipeline(explain.stages, function (stage) {
        return stage.hasOwnProperty("$sampleFromRandomCursor");
    });

    assert(randomSampleStage,
        "Expected $sampleFromRandomCursor stage, but not found");

    print("Found $sampleFromRandomCursor stage.");

    // 2. Must NOT have SORT
    var sortStage = findStageInPipeline(explain.stages, function (stage) {
        return stage.hasOwnProperty("$sort") ||
            stage.stage === "SORT";
    });

    assert(!sortStage,
        "Unexpected SORT stage found; slow path detected");

    // 3. Extract docsExamined from $cursor stage
    var cursorStage = findStageInPipeline(explain.stages, function (stage) {
        return stage.hasOwnProperty("$cursor");
    });

    assert(cursorStage, "No $cursor stage found");

    var execStats = cursorStage.$cursor.executionStats;
    assert(execStats, "No executionStats in $cursor stage");

    var docsExamined = execStats.totalDocsExamined;

    print("docsExamined = " + docsExamined);

    // 4. Random Cursor expectation
    assert(docsExamined === 0,
        "Random Cursor path should examine 0 documents, got " + docsExamined);

    print("PASS: $sample is using Random Cursor fast path (MongoDB 4.0)");

})();
