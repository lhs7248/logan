package test.logan.dianping.com.logan;
import org.junit.Test;
import static org.junit.Assert.*;
public final class WorkbenchSubmissionTest {
 @Test public void timeoutRestoresControlsWithoutClaimingTransportCancelled(){
  WorkbenchSubmission controller=new WorkbenchSubmission();
  WorkbenchSubmission.Attempt attempt=controller.begin("capture-a","2026-10-23",100);
  assertFalse(controller.timeout(attempt,30099));
  assertTrue(controller.timeout(attempt,30100));
  assertFalse(controller.busy());
  assertFalse(controller.timeout(attempt,30101));
  assertTrue(controller.complete(attempt)); // Same submission may receive a late receipt.
 }
 @Test public void completedSubmissionCannotLaterTimeOut(){
  WorkbenchSubmission controller=new WorkbenchSubmission();
  WorkbenchSubmission.Attempt attempt=controller.begin("capture-a","2026-10-23",0);
  assertTrue(controller.complete(attempt));
  assertFalse(controller.timeout(attempt,30000));
  assertFalse(controller.busy());
 }
 @Test public void newerCaptureRejectsLateReceiptAndPreservesSubmissionMarker(){
  WorkbenchSubmission controller=new WorkbenchSubmission();
  WorkbenchSubmission.Attempt old=controller.begin("capture-a","2026-10-23",0);
  controller.invalidate();
  assertEquals("capture-a",old.marker);
  assertFalse(controller.complete(old));
 }
 @Test public void newerUploadRejectsOldTimeoutAndOldReceipt(){
  WorkbenchSubmission controller=new WorkbenchSubmission();
  WorkbenchSubmission.Attempt old=controller.begin("capture-a","2026-10-23",0);
  controller.timeout(old,30000);
  WorkbenchSubmission.Attempt current=controller.begin("capture-b","2026-10-24",40000);
  assertNotEquals(old.id,current.id);
  assertFalse(controller.complete(old));
  assertFalse(controller.timeout(old,80000));
  assertTrue(controller.busy());
  assertTrue(controller.complete(current));
  assertFalse(controller.busy());
  assertFalse(controller.complete(current));
 }
}
